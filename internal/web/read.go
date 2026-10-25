package web

import (
	"encoding/json"
	"net/http"

	"github.com/ldgnu/rssurrection/internal/freshrss"
)

// handleRead marks a single article as read (or unread) in FreshRSS.
// It is POST-only and same-origin so that a GET prefetch can never change state.
func (s *Server) handleRead(w http.ResponseWriter, r *http.Request) {
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
		Key  string `json:"key"`
		Read bool   `json:"read"`
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
	if err := s.src.MarkRead(r.Context(), []string{key}, req.Read); err != nil {
		s.log.Warn("mark read failed", "kind", freshrss.Kind(err), "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "freshRSS rejected the change"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "read": req.Read})
}
