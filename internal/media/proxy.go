// Package media fetches remote images through RSSurrection so the browser
// never talks to third-party hosts: no mixed content, no hotlink blocks and
// no referrer leaks.
//
// Every URL is HMAC-signed, and every outbound connection is checked against
// a public-IP allow rule to prevent SSRF.
package media

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

const (
	defaultCacheTTL     = 6 * time.Hour
	defaultCacheEntries = 240
	maxImageBytes       = 6 << 20
	maxHTMLBytes        = 512 << 10
	maxRedirects        = 3
)

// Proxy serves signed image and open-graph requests.
type Proxy struct {
	key   []byte
	hc    *http.Client
	ttl   time.Duration
	max   int
	mu    sync.Mutex
	cache map[string]entry
	now   func() time.Time
}

type entry struct {
	data  []byte
	ctype string
	exp   time.Time
}

// New builds a proxy. The signing key must be stable across restarts, or
// every cached page would carry dead image URLs.
func New(secret string) *Proxy {
	key := sha256.Sum256([]byte(secret))
	return &Proxy{
		key:   key[:],
		hc:    newClient(),
		ttl:   defaultCacheTTL,
		max:   defaultCacheEntries,
		cache: make(map[string]entry),
		now:   time.Now,
	}
}

func newClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || !isPublicIP(ip) {
				return errors.New("media: blocked non-public address")
			}
			return nil
		},
	}
	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   6 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   12 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("media: too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("media: unsupported redirect")
			}
			return nil
		},
	}
}

func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// CGNAT 100.64.0.0/10 (also used by Tailscale).
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
		return true
	}
	// IPv6 unique local addresses fc00::/7.
	if len(ip) == net.IPv6len && (ip[0]&0xfe) == 0xfc {
		return false
	}
	return true
}

// Sign returns the signature for a target URL.
func (p *Proxy) Sign(raw string) string {
	m := hmac.New(sha256.New, p.key)
	m.Write([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))[:24]
}

func (p *Proxy) valid(raw, sig string) bool {
	return sig != "" && hmac.Equal([]byte(p.Sign(raw)), []byte(sig))
}

// ImageURL builds a signed same-origin URL for a remote image.
func (p *Proxy) ImageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return "/img?u=" + url.QueryEscape(raw) + "&k=" + p.Sign(raw)
}

// OpenGraphURL builds a signed URL that resolves the article's og:image.
func (p *Proxy) OpenGraphURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return "/img/og?u=" + url.QueryEscape(raw) + "&k=" + p.Sign(raw)
}

func validTarget(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("malformed url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("unsupported scheme")
	}
	if u.Host == "" {
		return nil, errors.New("missing host")
	}
	if u.User != nil {
		return nil, errors.New("credentials not allowed")
	}
	return u, nil
}

func (p *Proxy) cacheGet(key string) (entry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.cache[key]
	if !ok || p.now().After(e.exp) {
		return entry{}, false
	}
	return e, true
}

func (p *Proxy) cachePut(key string, e entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.cache) >= p.max {
		for k := range p.cache {
			delete(p.cache, k)
			break
		}
	}
	e.exp = p.now().Add(p.ttl)
	p.cache[key] = e
}

// HandleImage serves /img.
func (p *Proxy) HandleImage(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("u")
	if raw == "" || !p.valid(raw, r.URL.Query().Get("k")) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if _, err := validTarget(raw); err != nil {
		http.Error(w, "bad target", http.StatusBadRequest)
		return
	}
	if e, ok := p.cacheGet(raw); ok {
		writeImage(w, e)
		return
	}
	data, ctype, err := p.fetch(raw)
	if err != nil {
		http.Error(w, "cannot fetch image", http.StatusBadGateway)
		return
	}
	e := entry{data: data, ctype: ctype}
	p.cachePut(raw, e)
	writeImage(w, e)
}

func (p *Proxy) fetch(raw string) ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "RSSurrection/0.1 (+https://github.com/ldgnu/rssurrection)")
	req.Header.Set("Accept", "image/*")

	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", errors.New("upstream status")
	}
	ctype := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if !strings.HasPrefix(ctype, "image/") {
		return nil, "", errors.New("not an image")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxImageBytes {
		return nil, "", errors.New("image too large")
	}
	if len(data) == 0 {
		return nil, "", errors.New("empty image")
	}
	return data, ctype, nil
}

func writeImage(w http.ResponseWriter, e entry) {
	w.Header().Set("Content-Type", e.ctype)
	w.Header().Set("Content-Length", itoa(len(e.data)))
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(e.data)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// HandleOpenGraph resolves the article's preview image and redirects to it.
func (p *Proxy) HandleOpenGraph(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("u")
	if raw == "" || !p.valid(raw, r.URL.Query().Get("k")) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	base, err := validTarget(raw)
	if err != nil {
		http.Error(w, "bad target", http.StatusBadRequest)
		return
	}
	if img, ok := p.cacheGet("og:" + raw); ok {
		http.Redirect(w, r, p.ImageURL(string(img.data)), http.StatusFound)
		return
	}
	found, err := p.openGraphImage(base)
	if err != nil || found == "" {
		http.Error(w, "no preview image", http.StatusNotFound)
		return
	}
	p.cachePut("og:"+raw, entry{data: []byte(found), ctype: "text/plain"})
	http.Redirect(w, r, p.ImageURL(found), http.StatusFound)
}

func (p *Proxy) openGraphImage(base *url.URL) (string, error) {
	req, err := http.NewRequest(http.MethodGet, base.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "RSSurrection/0.1 (+https://github.com/ldgnu/rssurrection)")
	req.Header.Set("Accept", "text/html")
	resp, err := p.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("upstream status")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLBytes))
	if err != nil {
		return "", err
	}
	if !utf8.Valid(body) {
		body = []byte(strings.ToValidUTF8(string(body), ""))
	}
	return extractPreview(body, base)
}

// extractPreview looks for og:image, twitter:image and link rel=image_src.
func extractPreview(body []byte, base *url.URL) (string, error) {
	z := html.NewTokenizer(strings.NewReader(string(body)))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return "", errors.New("no preview image")
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			switch tok.Data {
			case "meta":
				var prop, name, content string
				for _, a := range tok.Attr {
					switch strings.ToLower(a.Key) {
					case "property":
						prop = strings.ToLower(strings.TrimSpace(a.Val))
					case "name":
						name = strings.ToLower(strings.TrimSpace(a.Val))
					case "content":
						content = strings.TrimSpace(a.Val)
					}
				}
				if content == "" {
					continue
				}
				if prop == "og:image" || prop == "og:image:url" || prop == "og:image:secure_url" ||
					name == "twitter:image" || name == "twitter:image:src" {
					if u := absoluteURL(content, base); u != "" {
						return u, nil
					}
				}
			case "link":
				var rel, href string
				for _, a := range tok.Attr {
					switch strings.ToLower(a.Key) {
					case "rel":
						rel = strings.ToLower(strings.TrimSpace(a.Val))
					case "href":
						href = strings.TrimSpace(a.Val)
					}
				}
				if rel == "image_src" && href != "" {
					if u := absoluteURL(href, base); u != "" {
						return u, nil
					}
				}
			}
		}
	}
}

func absoluteURL(raw string, base *url.URL) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(u)
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return ""
	}
	return abs.String()
}
