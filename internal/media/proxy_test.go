package media

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSignRejectsTampering(t *testing.T) {
	p := New("test-secret")
	u := "https://example.com/a.jpg"
	sig := p.Sign(u)
	if !p.valid(u, sig) {
		t.Fatal("valid signature rejected")
	}
	if p.valid(u+"?x=1", sig) {
		t.Fatal("tampered url accepted")
	}
	if p.valid(u, sig[:len(sig)-1]+"x") {
		t.Fatal("tampered signature accepted")
	}
}

func TestValidTarget(t *testing.T) {
	bad := []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"https://user:pass@example.com/x.png",
		"//example.com/x.png",
		"/relative.png",
		"ftp://example.com/x.png",
	}
	for _, raw := range bad {
		if _, err := validTarget(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	if _, err := validTarget("https://example.com/x.png"); err != nil {
		t.Fatalf("rejected valid url: %v", err)
	}
}

func TestPublicIPRule(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":    false,
		"10.1.2.3":     false,
		"192.168.1.1":  false,
		"172.16.0.1":   false,
		"169.254.1.1":  false,
		"100.64.0.1":   false,
		"0.0.0.0":      false,
		"8.8.8.8":      true,
		"1.1.1.1":      true,
		"2606:4700::1": true,
		"::1":          false,
		"fd00::1":      false,
	}
	for ip, want := range cases {
		got := isPublicIP(parseIP(t, ip))
		if got != want {
			t.Fatalf("isPublicIP(%s)=%v want %v", ip, got, want)
		}
	}
}

func TestExtractPreview(t *testing.T) {
	base, _ := url.Parse("https://news.example.com/post/1")
	html := `<html><head>
	<meta property="og:image" content="/img/hero.jpg">
	</head></html>`
	got, err := extractPreview([]byte(html), base)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://news.example.com/img/hero.jpg" {
		t.Fatalf("got %q", got)
	}

	twitter := `<meta name="twitter:image" content="https://cdn.example.com/t.png">`
	if got, _ := extractPreview([]byte(twitter), base); got != "https://cdn.example.com/t.png" {
		t.Fatalf("twitter image not used: %q", got)
	}

	if _, err := extractPreview([]byte(`<html><body>nada</body></html>`), base); err == nil {
		t.Fatal("expected error when there is no preview")
	}
}

func TestHandleImageServesCachedImage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nfake"))
	}))
	defer upstream.Close()

	p := New("test-secret")
	p.hc = upstream.Client() // bypass the public-IP dialer for this unit test

	raw := upstream.URL + "/a.png"
	req := httptest.NewRequest(http.MethodGet, p.ImageURL(raw), nil)
	rec := httptest.NewRecorder()
	p.HandleImage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Fatalf("content type %q", ct)
	}
}

func TestHandleImageRejectsHTML(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer upstream.Close()

	p := New("test-secret")
	p.hc = upstream.Client()

	req := httptest.NewRequest(http.MethodGet, p.ImageURL(upstream.URL+"/a"), nil)
	rec := httptest.NewRecorder()
	p.HandleImage(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

func TestHandleImageRejectsLoopbackInProduction(t *testing.T) {
	p := New("test-secret") // default client enforces the public-IP rule
	raw := "http://127.0.0.1:9/x.png"
	req := httptest.NewRequest(http.MethodGet, p.ImageURL(raw), nil)
	rec := httptest.NewRecorder()
	p.HandleImage(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for loopback target, got %d", rec.Code)
	}
}

func TestHandleImageRejectsBadSignature(t *testing.T) {
	p := New("test-secret")
	req := httptest.NewRequest(http.MethodGet, "/img?u=https%3A%2F%2Fexample.com%2Fa.png&k=nope", nil)
	rec := httptest.NewRecorder()
	p.HandleImage(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func parseIP(t *testing.T, s string) (ip netIP) {
	t.Helper()
	parsed := netParseIP(s)
	if parsed == nil {
		t.Fatalf("bad ip %s", s)
	}
	return parsed
}

type netIP = net.IP

func netParseIP(s string) net.IP { return net.ParseIP(s) }
