package web

import (
	"encoding/json"
	"net/http"
	"time"
)

// handleClientLog receives lightweight diagnostics from the browser so
// failures on real devices (iOS) can be observed from the server log.
func (s *Server) handleClientLog(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.log.Info("client", "kind", payload["kind"], "build", payload["build"],
		"js", payload["js"], "detail", payload["detail"])
	w.WriteHeader(http.StatusNoContent)
}

// handleRefresh clears the browser cache with a header (no JS needed) and
// sends the user back to a freshly fetched app.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Clear-Site-Data", `"cache"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html lang="es"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="refresh" content="1;url=/">
<title>Actualizando</title>
<style>body{font-family:system-ui;background:#faf5ec;color:#2b211a;display:flex;
align-items:center;justify-content:center;height:100vh;margin:0;text-align:center}
code{background:#0001;padding:.2rem .4rem;border-radius:4px}</style></head>
<body><div><p>Limpiando caché…</p><p><small>build <code>` + s.assetVer + `</code></small></p>
<p><a href="/">Continuar</a></p></div></body></html>`))
}

var _ = time.Now
