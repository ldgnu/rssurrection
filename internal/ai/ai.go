// Package ai produces short summaries through an OpenAI-compatible endpoint.
// It is optional: without configuration RSSurrection never mentions it.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultBase  = "https://openrouter.ai/api/v1"
	maxBodyBytes = 1 << 20
	maxInput     = 3500
)

// Client is a minimal chat-completions client.
type Client struct {
	base  string
	key   string
	model string
	hc    *http.Client
}

// Options configures the client.
type Options struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// New validates the options.
func New(o Options) (*Client, error) {
	if strings.TrimSpace(o.APIKey) == "" {
		return nil, errors.New("ai: missing API key")
	}
	if strings.TrimSpace(o.Model) == "" {
		return nil, errors.New("ai: missing model")
	}
	base := strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")
	if base == "" {
		base = defaultBase
	}
	if !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://") {
		return nil, errors.New("ai: base URL must be http(s)")
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		base:  base,
		key:   o.APIKey,
		model: o.Model,
		hc:    &http.Client{Timeout: timeout},
	}, nil
}

// Model returns the configured model name (safe to display).
func (c *Client) Model() string { return c.model }

const systemPrompt = `Decí de qué trata la nota en dos oraciones, en español rioplatense neutro. ` +
	`Explicá el tema, el enfoque y por qué puede interesar. NO cuentes el desenlace, las conclusiones, ` +
	`los resultados ni datos del final: nada de spoilers. No repitas el titular. ` +
	`Si el texto no alcanza, respondé exactamente: SIN_CONTENIDO.`

// Summarize returns a short summary of the given title and body text.
func (c *Client) Summarize(ctx context.Context, title, body string) (string, error) {
	body = strings.TrimSpace(body)
	if len([]rune(body)) < 120 {
		return "", errors.New("ai: not enough text to summarize")
	}
	if len(body) > maxInput {
		body = body[:maxInput]
	}
	payload := map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": "Titular: " + title + "\n\nTexto:\n" + body},
		},
		"temperature": 0.2,
		"max_tokens":  900,
		"stream":      false,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("User-Agent", "RSSurrection/0.1 (+https://github.com/ldgnu/rssurrection)")
	req.Header.Set("HTTP-Referer", "https://github.com/ldgnu/rssurrection")
	req.Header.Set("X-Title", "RSSurrection")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("ai: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body2, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("ai: cannot read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ai: upstream status %d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body2, &out); err != nil {
		return "", fmt.Errorf("ai: invalid response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("ai: empty response")
	}
	text := strings.TrimSpace(out.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("ai: empty content (finish=%q, reasoning_len=%d, body=%d)",
			out.Choices[0].FinishReason, len(out.Choices[0].Message.ReasoningContent), len(body2))
	}
	// The model is instructed to answer exactly SIN_CONTENIDO when the source
	// has nothing to summarize; only that exact marker is rejected.
	if strings.EqualFold(text, "SIN_CONTENIDO") || strings.EqualFold(text, "SIN CONTENIDO") {
		return "", errors.New("ai: nothing to summarize")
	}
	return text, nil
}
