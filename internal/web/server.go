// Package web renders RSSurrection's server-side UI.
package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ldgnu/rssurrection/internal/ai"
	"github.com/ldgnu/rssurrection/internal/buildinfo"
	"github.com/ldgnu/rssurrection/internal/config"
	"github.com/ldgnu/rssurrection/internal/freshrss"
	"github.com/ldgnu/rssurrection/internal/media"
	"github.com/ldgnu/rssurrection/internal/regional"
)

//go:embed templates/*.html static/*
var assets embed.FS

const (
	diagTTL      = 30 * time.Second
	contentType  = "text/html; charset=utf-8"
	maxPageBytes = 4 << 20
)

// DataSource is the read-only subset of the FreshRSS client the UI needs.
type DataSource interface {
	Diag(ctx context.Context) (freshrss.Diag, error)
	StreamContents(ctx context.Context, q freshrss.StreamQuery) (freshrss.StreamPage, error)
	Items(ctx context.Context, keys []string) ([]freshrss.Article, error)
	Tags(ctx context.Context) ([]freshrss.Tag, error)
	SetStarred(ctx context.Context, keys []string, starred bool) error
	MarkRead(ctx context.Context, keys []string, read bool) error
	Subscriptions(ctx context.Context) ([]freshrss.Subscription, error)
	UnreadCounts(ctx context.Context) (freshrss.UnreadCounts, error)
	RenameFeed(ctx context.Context, feedID, title string) error
}

// Server holds the HTTP handlers and rendered templates.
type Server struct {
	cfg      config.Config
	log      *slog.Logger
	src      DataSource
	media    *media.Proxy
	ai       *ai.Client
	regional *regional.Fetcher

	limiter *loginLimiter

	sumMu       sync.Mutex
	summaries   map[string]summaryEntry
	sumInflight map[string]bool

	trMu         sync.Mutex
	translations map[string]translationEntry
	pages        map[string]*template.Template
	handler      http.Handler
	started      time.Time

	diagMu   sync.Mutex
	diagVal  freshrss.Diag
	diagErr  error
	diagAt   time.Time
	diagSet  bool
	diagBusy chan struct{}
	assetVer string

	catsMu  sync.Mutex
	cats    []categoryView
	catsAt  time.Time
	catsSet bool
}

// New builds the server. diagFn may be nil when FreshRSS is not configured.
func New(cfg config.Config, log *slog.Logger, src DataSource, mp *media.Proxy, aiClient *ai.Client, rf *regional.Fetcher) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{cfg: cfg, log: log, src: src, media: mp, ai: aiClient, regional: rf,
		summaries:    make(map[string]summaryEntry),
		sumInflight:  make(map[string]bool),
		translations: make(map[string]translationEntry),
		started:      time.Now()}

	funcs := template.FuncMap{
		"since":  humanSince,
		"when":   humanTime,
		"fmtDur": func(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) + " ms" },
	}
	s.limiter = &loginLimiter{fails: make(map[string][]time.Time)}
	s.assetVer = s.assetVersion()
	s.pages = make(map[string]*template.Template, 4)
	for _, name := range []string{"home", "setup", "diag", "error", "article", "feeds", "categorias", "ajustes", "menu", "login", "acciones"} {
		files := []string{"templates/layout.html", "templates/" + name + ".html"}
		if name == "home" {
			files = append(files, "templates/cards.html")
		}
		t, err := template.New("layout").Funcs(funcs).ParseFS(assets, files...)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		s.pages[name] = t
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /sw.js", s.handleServiceWorker)
	mux.HandleFunc("GET /refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/client-log", s.handleClientLog)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /diag", s.handleDiag)
	mux.HandleFunc("GET /a/{key}", s.handleArticle)
	mux.HandleFunc("GET /c/{slug}", s.handleCategory)
	mux.HandleFunc("GET /guardados", s.handleSaved)
	mux.HandleFunc("GET /feeds", s.handleFeeds)
	mux.HandleFunc("GET /categorias", s.handleCategories)
	mux.HandleFunc("GET /regional", s.handleRegional)
	mux.HandleFunc("GET /ajustes", s.handleSettings)
	mux.HandleFunc("GET /menu", s.handleMenu)
	mux.HandleFunc("GET /a/{key}/opciones", s.handleArticleActions)
	mux.HandleFunc("POST /a/{key}/star", s.readerAction("star"))
	mux.HandleFunc("POST /a/{key}/read", s.readerAction("read"))
	mux.HandleFunc("GET /font", s.handleFont)
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("POST /feeds/rename", s.handleRenameFeed)
	mux.HandleFunc("POST /api/summary", s.handleSummary)
	mux.HandleFunc("POST /api/translate", s.handleTranslate)
	mux.HandleFunc("POST /api/star", s.handleStar)
	mux.HandleFunc("POST /api/read", s.handleRead)
	mux.HandleFunc("GET /api/feed", s.handleFeed)
	if mp != nil {
		mux.HandleFunc("GET /img", mp.HandleImage)
		mux.HandleFunc("GET /img/og", mp.HandleOpenGraph)
	}
	mux.HandleFunc("GET /{$}", s.handleHome)

	static, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	fileServer := http.FileServer(http.FS(static))
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheControl(manifestType(fileServer))))
	mux.HandleFunc("/", s.handleNotFound)

	s.handler = chain(mux,
		s.recoverPanic,
		s.securityHeaders,
		s.logRequests,
		s.sessionAuth,
	)
	return s, nil
}

// assetVersion hashes the embedded CSS and JS so their URLs can change on
// every deploy. iOS PWAs cache aggressively; this is the only reliable way to
// make a new build land.
func (s *Server) assetVersion() string {
	h := sha256.New()
	for _, name := range []string{"static/app.css", "static/app.js", "static/theme.js"} {
		if b, err := assets.ReadFile(name); err == nil {
			h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	body, err := assets.ReadFile("static/sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	out := strings.ReplaceAll(string(body), "__VERSION__", s.assetVer)
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Service-Worker-Allowed", "/")
	_, _ = w.Write([]byte(out))
}

// Handler returns the root http.Handler.
func (s *Server) Handler() http.Handler { return s.handler }

type pageData struct {
	Title      string
	Nav        string
	Version    string
	Configured bool
	BaseURL    string
	User       string
	Diag       freshrss.Diag
	DiagErr    string
	DiagKind   string
	HasDiag    bool
	Now        time.Time

	Articles        []articleView
	Pages           [][]articleView
	Next            string
	Article         *articleView
	Content         template.HTML
	Categories      []categoryView
	Category        string
	CategorySlug    string
	Mosaic          bool
	ToggleHref      string
	PrevHref        string
	NextHref        string
	Bare            bool
	AssetVer        string
	Stream          bool
	Saved           bool
	AIEnabled       bool
	TranslateToggle bool
	LoginError      string
	LoginNext       string
	FontSize        string
	Notice          string
	AIModel         string
	Regional        []string
}

func (s *Server) baseData(title, nav string) pageData {
	return pageData{
		Title:      title,
		Nav:        nav,
		Version:    buildinfo.Version,
		Configured: s.cfg.FreshRSS.Configured(),
		AssetVer:   s.assetVer,
		BaseURL:    s.diagBaseURL(),
		User:       s.cfg.FreshRSS.User,
		Now:        time.Now(),
	}
}

func (s *Server) diagBaseURL() string {
	if !s.cfg.FreshRSS.Configured() {
		return ""
	}
	return s.cfg.FreshRSS.BaseURL.String()
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

type readyResponse struct {
	Status   string `json:"status"`
	FreshRSS string `json:"freshrss"`
	Version  string `json:"version"`
	UptimeS  int64  `json:"uptime_s"`
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	resp := readyResponse{
		Status:   "ok",
		FreshRSS: "configured",
		Version:  buildinfo.Version,
		UptimeS:  int64(time.Since(s.started).Seconds()),
	}
	if !s.cfg.FreshRSS.Configured() {
		// The process is ready to serve the setup screen, but degraded.
		resp.Status = "degraded"
		resp.FreshRSS = "missing"
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleDiag(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.FreshRSS.Configured() {
		s.render(w, r, http.StatusServiceUnavailable, "setup", s.baseData("Configuración pendiente", "diag"))
		return
	}
	data := s.baseData("Diagnóstico", "diag")
	d, err := s.cachedDiag(r.Context(), true)
	data.Diag, data.HasDiag = d, true
	if err != nil {
		data.DiagErr = friendlyError(err)
		data.DiagKind = freshrss.Kind(err)
	}
	s.render(w, r, http.StatusOK, "diag", data)
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	data := s.baseData("No encontrado", "")
	data.DiagErr = "La página que buscás no existe."
	s.render(w, r, http.StatusNotFound, "error", data)
}

func (s *Server) cachedDiag(ctx context.Context, force bool) (freshrss.Diag, error) {
	if s.src == nil {
		return freshrss.Diag{}, errors.New("FreshRSS no está configurado")
	}
	s.diagMu.Lock()
	if !force && s.diagSet && time.Since(s.diagAt) < diagTTL {
		d, err := s.diagVal, s.diagErr
		s.diagMu.Unlock()
		return d, err
	}
	if s.diagBusy != nil {
		wait := s.diagBusy
		s.diagMu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return freshrss.Diag{}, ctx.Err()
		}
		return s.cachedDiag(ctx, false)
	}
	done := make(chan struct{})
	s.diagBusy = done
	s.diagMu.Unlock()

	d, err := s.src.Diag(ctx)

	s.diagMu.Lock()
	s.diagVal, s.diagErr, s.diagAt, s.diagSet = d, err, time.Now(), true
	s.diagBusy = nil
	close(done)
	s.diagMu.Unlock()
	return d, err
}

func (s *Server) invalidateTags() {
	s.catsMu.Lock()
	s.catsSet = false
	s.catsMu.Unlock()
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, data pageData) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "template missing", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.log.Error("render failed", "page", page, "error", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	out := buf.Bytes()
	if len(out) > maxPageBytes {
		s.log.Error("page too large", "page", page, "bytes", len(out))
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(out)
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		s.log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"bytes", sw.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic recovered", "path", r.URL.Path, "panic", fmt.Sprint(rec))
				http.Error(w, "error interno", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; "+
				"base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// manifestType gives the web app manifest its registered media type; Go's
// mime table does not know .webmanifest on every platform.
func manifestType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".webmanifest") {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		next.ServeHTTP(w, r)
	})
}

func cacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}

func chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

func friendlyError(err error) string {
	if err == nil {
		return ""
	}
	switch freshrss.Kind(err) {
	case "unauthorized":
		return "FreshRSS rechazó las credenciales. Revisá FRESHRSS_USER y FRESHRSS_API_PASSWORD (es la contraseña de API, no la del usuario)."
	case "unreachable":
		return "No se pudo llegar a FreshRSS. Verificá FRESHRSS_BASE_URL, la red del contenedor y el certificado TLS."
	case "timeout":
		return "FreshRSS no respondió dentro del tiempo configurado."
	case "response_too_large":
		return "La respuesta de FreshRSS superó el límite configurado en FRESHRSS_MAX_RESPONSE_BYTES."
	case "invalid_response":
		return "FreshRSS devolvió una respuesta que no se pudo interpretar. ¿FRESHRSS_BASE_URL apunta a /api/greader.php?"
	case "not_found":
		return "El endpoint de la API no existe en esa URL."
	default:
		var msg strings.Builder
		msg.WriteString("Falló la consulta a FreshRSS.")
		if err != nil {
			msg.WriteString(" Detalle: ")
			msg.WriteString(truncate(err.Error(), 300))
		}
		return msg.String()
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var monthES = [...]string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}

func humanTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return fmt.Sprintf("%d %s %d, %02d:%02d", t.Day(), monthES[int(t.Month())-1], t.Year(), t.Hour(), t.Minute())
}

func humanSince(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "recién"
	case d < time.Hour:
		return fmt.Sprintf("hace %d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("hace %d h", int(d.Hours()))
	default:
		return fmt.Sprintf("hace %d días", int(d.Hours()/24))
	}
}
