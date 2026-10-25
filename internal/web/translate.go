package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	translateTTL = 30 * 24 * time.Hour
	maxTexts     = 12
	maxTextLen   = 400
)

type translationEntry struct {
	text string
	at   time.Time
}

func (s *Server) cachedTranslation(src string) (string, bool) {
	s.trMu.Lock()
	defer s.trMu.Unlock()
	e, ok := s.translations[src]
	if !ok || time.Since(e.at) > translateTTL {
		return "", false
	}
	return e.text, true
}

func (s *Server) storeTranslation(src, out string) {
	s.trMu.Lock()
	defer s.trMu.Unlock()
	if len(s.translations) > 2000 {
		for k := range s.translations {
			delete(s.translations, k)
			break
		}
	}
	s.translations[src] = translationEntry{text: out, at: time.Now()}
}

// handleTranslate translates short texts (titles, summaries) to Spanish.
func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	if s.ai == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "ai not configured"})
		return
	}
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin request refused"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var req struct {
		Texts []string `json:"texts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	if len(req.Texts) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"texts": []string{}})
		return
	}
	if len(req.Texts) > maxTexts {
		req.Texts = req.Texts[:maxTexts]
	}

	out := make([]string, len(req.Texts))
	var missing []int
	var missingTexts []string
	for i, t := range req.Texts {
		t = strings.TrimSpace(t)
		if t == "" || len([]rune(t)) > maxTextLen {
			out[i] = t
			continue
		}
		if cached, ok := s.cachedTranslation(t); ok {
			out[i] = cached
			continue
		}
		missing = append(missing, i)
		missingTexts = append(missingTexts, t)
	}
	if len(missingTexts) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"texts": out, "cached": true})
		return
	}

	translated, err := s.ai.Translate(r.Context(), missingTexts)
	if err != nil || len(translated) != len(missingTexts) {
		s.log.Warn("translate failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "cannot translate"})
		return
	}
	for k, idx := range missing {
		out[idx] = translated[k]
		s.storeTranslation(missingTexts[k], translated[k])
	}
	writeJSON(w, http.StatusOK, map[string]any{"texts": out})
}
