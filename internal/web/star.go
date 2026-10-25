package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/ldgnu/rssurrection/internal/freshrss"
)

const maxStarBody = 2 << 10

// sameOrigin rejects cross-site writes. Browsers always send Origin on POST;
// non-browser clients (curl) may omit it and are allowed through.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func (s *Server) handleStar(w http.ResponseWriter, r *http.Request) {
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
		Key     string `json:"key"`
		Starred bool   `json:"starred"`
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
	if err := s.src.SetStarred(r.Context(), []string{key}, req.Starred); err != nil {
		s.log.Warn("star failed", "kind", freshrss.Kind(err), "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "freshRSS rejected the change"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "starred": req.Starred})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
