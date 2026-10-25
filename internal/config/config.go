// Package config loads and validates RSSurrection's runtime configuration.
//
// Every value comes from the environment: nothing is hardcoded and no
// credential is ever written to disk by the application.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListenAddr       = ":8080"
	defaultTimeout          = 10 * time.Second
	maxTimeout              = 60 * time.Second
	defaultMaxResponseBytes = 8 << 20
	minMaxResponseBytes     = 64 << 10
	maxMaxResponseBytes     = 32 << 20
	defaultShutdownTimeout  = 10 * time.Second
)

// Config is the fully validated application configuration.
type Config struct {
	ListenAddr      string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
	FreshRSS        FreshRSS
	AI              AI
	Regional        []RegionalSource
	RegionalLimit   int
	SigningSecret   string
}

// RegionalSource is a public news feed outside FreshRSS.
type RegionalSource struct {
	Name string
	URL  string
}

// AI configures the optional summary model (OpenAI-compatible).
type AI struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// Configured reports whether summaries can be generated.
func (a AI) Configured() bool { return a.APIKey != "" && a.Model != "" }

// LogValue implements slog.LogValuer without exposing the key.
func (a AI) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Bool("configured", a.Configured()),
		slog.String("model", a.Model),
		slog.Bool("api_key_set", a.APIKey != ""),
	)
}

// FreshRSS holds everything needed to talk to a FreshRSS instance.
//
// BaseURL is the normalized greader endpoint (…/api/greader.php). A nil
// BaseURL means "not configured": the app still starts and shows the setup
// screen instead of pretending to have news.
type FreshRSS struct {
	BaseURL          *url.URL
	User             string
	APIPassword      string
	Timeout          time.Duration
	MaxResponseBytes int64
}

// Configured reports whether a FreshRSS endpoint was provided.
func (f FreshRSS) Configured() bool { return f.BaseURL != nil }

// String implements fmt.Stringer without ever revealing the API password.
func (f FreshRSS) String() string {
	if !f.Configured() {
		return "freshrss{configured=false}"
	}
	return fmt.Sprintf("freshrss{base=%q user=%q password=REDACTED}", f.BaseURL.String(), f.User)
}

// LogValue implements slog.LogValuer.
func (f FreshRSS) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Bool("configured", f.Configured()),
		slog.String("base_url", safeURL(f.BaseURL)),
		slog.String("user", f.User),
		slog.Bool("password_set", f.APIPassword != ""),
	)
}

func safeURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.String()
}

// Lookup is the environment accessor used by Load.
type Lookup func(string) string

// FromEnv loads the configuration from the process environment.
func FromEnv() (Config, error) { return Load(os.Getenv) }

// Load builds a validated Config from getenv.
func Load(getenv Lookup) (Config, error) {
	cfg := Config{
		ListenAddr:      defaultListenAddr,
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: defaultShutdownTimeout,
	}
	var errs []error

	if v := strings.TrimSpace(getenv("RSSURRECTION_LISTEN_ADDR")); v != "" {
		if err := validateListenAddr(v); err != nil {
			errs = append(errs, fmt.Errorf("RSSURRECTION_LISTEN_ADDR: %w", err))
		} else {
			cfg.ListenAddr = v
		}
	}

	if v := strings.TrimSpace(getenv("RSSURRECTION_LOG_LEVEL")); v != "" {
		lvl, err := parseLevel(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("RSSURRECTION_LOG_LEVEL: %w", err))
		} else {
			cfg.LogLevel = lvl
		}
	}

	if v := strings.TrimSpace(getenv("RSSURRECTION_SHUTDOWN_TIMEOUT")); v != "" {
		d, err := parseDuration("RSSURRECTION_SHUTDOWN_TIMEOUT", v, time.Second, 60*time.Second)
		if err != nil {
			errs = append(errs, err)
		} else {
			cfg.ShutdownTimeout = d
		}
	}

	fr, err := loadFreshRSS(getenv)
	if err != nil {
		errs = append(errs, err)
	}
	cfg.FreshRSS = fr
	cfg.AI = loadAI(getenv)
	cfg.Regional, cfg.RegionalLimit = loadRegional(getenv)
	cfg.SigningSecret = loadSigningSecret(getenv, cfg.FreshRSS)

	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadFreshRSS(getenv Lookup) (FreshRSS, error) {
	rawURL := strings.TrimSpace(getenv("FRESHRSS_BASE_URL"))
	if rawURL == "" {
		// Widely used alias (FreshRSS mobile clients, MCP servers).
		rawURL = strings.TrimSpace(getenv("FRESHRSS_URL"))
	}
	user := strings.TrimSpace(getenv("FRESHRSS_USER"))
	pass := getenv("FRESHRSS_API_PASSWORD")

	set := 0
	for _, v := range []string{rawURL, user, pass} {
		if v != "" {
			set++
		}
	}
	switch set {
	case 0:
		return FreshRSS{}, nil // not configured on purpose
	case 3:
		// complete, validated below
	default:
		var missing []string
		if rawURL == "" {
			missing = append(missing, "FRESHRSS_BASE_URL")
		}
		if user == "" {
			missing = append(missing, "FRESHRSS_USER")
		}
		if pass == "" {
			missing = append(missing, "FRESHRSS_API_PASSWORD")
		}
		return FreshRSS{}, fmt.Errorf("incomplete FreshRSS configuration, missing: %s", strings.Join(missing, ", "))
	}

	allowInsecure := isTruthy(getenv("RSSURRECTION_ALLOW_INSECURE_FRESHRSS"))
	base, err := NormalizeBaseURL(rawURL, allowInsecure)
	if err != nil {
		return FreshRSS{}, fmt.Errorf("FRESHRSS_BASE_URL: %w", err)
	}

	timeout := defaultTimeout
	if v := strings.TrimSpace(getenv("FRESHRSS_TIMEOUT")); v != "" {
		d, err := parseDuration("FRESHRSS_TIMEOUT", v, time.Second, maxTimeout)
		if err != nil {
			return FreshRSS{}, err
		}
		timeout = d
	}

	maxBytes := int64(defaultMaxResponseBytes)
	if v := strings.TrimSpace(getenv("FRESHRSS_MAX_RESPONSE_BYTES")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return FreshRSS{}, fmt.Errorf("FRESHRSS_MAX_RESPONSE_BYTES: not an integer: %q", v)
		}
		if n < minMaxResponseBytes || n > maxMaxResponseBytes {
			return FreshRSS{}, fmt.Errorf("FRESHRSS_MAX_RESPONSE_BYTES: must be between %d and %d, got %d",
				minMaxResponseBytes, maxMaxResponseBytes, n)
		}
		maxBytes = n
	}

	return FreshRSS{
		BaseURL:          base,
		User:             user,
		APIPassword:      pass,
		Timeout:          timeout,
		MaxResponseBytes: maxBytes,
	}, nil
}

// NormalizeBaseURL validates a FreshRSS base URL and appends the Google
// Reader endpoint when the user only provided the site root.
//
// Accepted shapes:
//
//	https://rss.example.com
//	https://rss.example.com/
//	https://example.com/freshrss
//	https://example.com/freshrss/api/greader.php
//
// Rejected: credentials in the URL, query strings, fragments, non-HTTP
// schemes, and plain http to a non-local host unless allowInsecure is set.
func NormalizeBaseURL(raw string, allowInsecure bool) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty value")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("not a valid URL: %w", err)
	}
	if u.Host == "" {
		return nil, errors.New("missing host")
	}
	if u.User != nil {
		return nil, errors.New("must not contain credentials (use FRESHRSS_USER and FRESHRSS_API_PASSWORD)")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("must not contain a query string or fragment")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q (need http or https)", u.Scheme)
	}
	if u.Scheme == "http" && !allowInsecure && !isLocalHost(u.Hostname()) {
		return nil, errors.New("plain http to a non-local host is refused; use https or set RSSURRECTION_ALLOW_INSECURE_FRESHRSS=true for a trusted network")
	}

	p := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(p, "/api/greader.php") {
		p += "/api/greader.php"
	}
	u.Path = p
	u.RawPath = ""
	return u, nil
}

func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || h == "" {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func validateListenAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("expected host:port (for example :8080): %w", err)
	}
	if port == "" {
		return errors.New("missing port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid port %q", port)
	}
	if host != "" {
		if ip := net.ParseIP(host); ip == nil && host != "localhost" {
			return fmt.Errorf("invalid host %q", host)
		}
	}
	return nil
}

func parseDuration(name, raw string, min, max time.Duration) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: not a duration (for example 10s): %w", name, err)
	}
	if d < min || d > max {
		return 0, fmt.Errorf("%s: must be between %s and %s, got %s", name, min, max, d)
	}
	return d, nil
}

func parseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown level %q (use debug, info, warn or error)", raw)
	}
}

func loadAI(getenv Lookup) AI {
	key := strings.TrimSpace(getenv("RSSURRECTION_AI_API_KEY"))
	if key == "" {
		key = strings.TrimSpace(getenv("OPENROUTER_API_KEY"))
	}
	model := strings.TrimSpace(getenv("RSSURRECTION_AI_MODEL"))
	if model == "" && key != "" {
		model = "meta-llama/llama-3.3-70b-instruct"
	}
	base := strings.TrimSpace(getenv("RSSURRECTION_AI_BASE_URL"))
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}
	timeout := 30 * time.Second
	if v := strings.TrimSpace(getenv("RSSURRECTION_AI_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 3*time.Second && d <= 120*time.Second {
			timeout = d
		}
	}
	return AI{BaseURL: base, APIKey: key, Model: model, Timeout: timeout}
}

var defaultRegional = []RegionalSource{
	{Name: "La Nación", URL: "https://www.lanacion.com.ar/arc/outboundfeeds/rss/"},
	{Name: "Ámbito", URL: "https://www.ambito.com/rss/pages/home.xml"},
	{Name: "Clarín", URL: "https://www.clarin.com/rss/lo-ultimo/"},
	{Name: "Perfil", URL: "https://www.perfil.com/feed"},
}

// loadRegional parses RSSURRECTION_REGIONAL_FEEDS ("Nombre|URL,Nombre|URL")
// and RSSURRECTION_REGIONAL_LIMIT. An empty value means "not configured".
func loadRegional(getenv Lookup) ([]RegionalSource, int) {
	limit := 12
	if v := strings.TrimSpace(getenv("RSSURRECTION_REGIONAL_LIMIT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 40 {
			limit = n
		}
	}
	raw := strings.TrimSpace(getenv("RSSURRECTION_REGIONAL_FEEDS"))
	if raw == "" {
		return defaultRegional, limit
	}
	if raw == "none" || raw == "off" {
		return nil, limit
	}
	var out []RegionalSource
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, link := "", part
		if i := strings.Index(part, "|"); i > 0 {
			name, link = strings.TrimSpace(part[:i]), strings.TrimSpace(part[i+1:])
		}
		u, err := url.Parse(link)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			continue
		}
		if name == "" {
			name = u.Host
		}
		out = append(out, RegionalSource{Name: name, URL: u.String()})
	}
	return out, limit
}

// loadSigningSecret keeps media/reader signatures valid across restarts.
// An explicit secret wins; otherwise it is derived from the FreshRSS
// credentials, which never leave the process.
func loadSigningSecret(getenv Lookup, fr FreshRSS) string {
	if v := strings.TrimSpace(getenv("RSSURRECTION_SIGNING_SECRET")); v != "" {
		return v
	}
	if !fr.Configured() {
		return "rssurrection-unconfigured"
	}
	return fr.BaseURL.String() + "|" + fr.User + "|" + fr.APIPassword
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
