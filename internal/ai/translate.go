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
)

const translatePrompt = `Traducí al español rioplatense cada línea del mensaje del usuario. ` +
	`Devolvé exactamente la misma cantidad de líneas, sin numerar, sin comillas y sin comentarios. ` +
	`No resumas ni interpretes: traducí.`

// Translate translates a batch of short texts, returning one line per input.
func (c *Client) Translate(ctx context.Context, texts []string) ([]string, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	payload := map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": translatePrompt},
			{"role": "user", "content": strings.Join(texts, "\n")},
		},
		"temperature": 0.1,
		"max_tokens":  1200,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("User-Agent", "RSSurrection/0.1 (+https://github.com/ldgnu/rssurrection)")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ai: translate request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ai: translate status %d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, errors.New("ai: empty translation response")
	}
	lines := strings.Split(strings.TrimSpace(out.Choices[0].Message.Content), "\n")
	var clean []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			clean = append(clean, l)
		}
	}
	if len(clean) != len(texts) {
		return nil, fmt.Errorf("ai: expected %d lines, got %d", len(texts), len(clean))
	}
	return clean, nil
}
