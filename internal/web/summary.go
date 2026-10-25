package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ldgnu/rssurrection/internal/freshrss"
	"github.com/ldgnu/rssurrection/internal/htmlsafe"
)

const summaryTTL = 7 * 24 * time.Hour

type summaryEntry struct {
	text string
	at   time.Time
}

func (s *Server) cachedSummary(key string) (string, bool) {
	s.sumMu.Lock()
	defer s.sumMu.Unlock()
	e, ok := s.summaries[key]
	if !ok || time.Since(e.at) > summaryTTL {
		return "", false
	}
	return e.text, true
}

func (s *Server) storeSummary(key, text string) {
	s.sumMu.Lock()
	defer s.sumMu.Unlock()
	if len(s.summaries) > 500 {
		for k := range s.summaries {
			delete(s.summaries, k)
			break
		}
	}
	s.summaries[key] = summaryEntry{text: text, at: time.Now()}
}

// warmSummaries pre-generates summaries in the background for the first
// articles of a feed page, so opening them feels instant. Bounded to a few
// items and deduplicated with in-flight requests.
func (s *Server) warmSummaries(keys []string) {
	if s.ai == nil || len(keys) == 0 {
		return
	}
	if len(keys) > 3 {
		keys = keys[:3]
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, ok := s.cachedSummary(key); ok {
			continue
		}
		s.sumMu.Lock()
		busy := s.sumInflight[key]
		if !busy {
			s.sumInflight[key] = true
		}
		s.sumMu.Unlock()
		if busy {
			continue
		}
		go func(key string) {
			defer func() {
				s.sumMu.Lock()
				delete(s.sumInflight, key)
				s.sumMu.Unlock()
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			items, err := s.src.Items(ctx, []string{key})
			if err != nil || len(items) == 0 {
				return
			}
			art := items[0]
			text, err := s.ai.Summarize(ctx, art.Title, htmlsafe.Text(art.ContentHTML))
			if err != nil {
				return
			}
			s.storeSummary(key, text)
			s.log.Debug("summary warmed", "key", key)
		}(key)
	}
}

// handleSummary returns a cached or freshly generated AI summary.
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	if s.ai == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "ai not configured"})
		return
	}
	if !s.cfg.FreshRSS.Configured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "freshrss not configured"})
		return
	}
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin request refused"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStarBody)
	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	key, err := freshrss.ParseItemKey(req.Key)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid item id"})
		return
	}
	if text, ok := s.cachedSummary(key); ok {
		writeJSON(w, http.StatusOK, map[string]any{"summary": text, "cached": true})
		return
	}

	items, err := s.src.Items(r.Context(), []string{key})
	if err != nil || len(items) == 0 {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "cannot load article"})
		return
	}
	art := items[0]
	plain := htmlsafe.Text(art.ContentHTML)

	text, err := s.ai.Summarize(r.Context(), art.Title, plain)
	if err != nil {
		s.log.Warn("summary failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "cannot summarize"})
		return
	}
	s.storeSummary(key, text)
	writeJSON(w, http.StatusOK, map[string]any{"summary": text, "model": s.ai.Model()})
}
