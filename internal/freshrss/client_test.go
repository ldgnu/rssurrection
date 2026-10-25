package freshrss

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.Handler, mod func(*Options)) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	base, err := url.Parse(srv.URL + "/api/greader.php")
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{BaseURL: base, User: "u", Password: "p", Timeout: 2 * time.Second}
	if mod != nil {
		mod(&opts)
	}
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSubscriptionList(t *testing.T) {
	var authSeen string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/accounts/ClientLogin"):
			_, _ = io.WriteString(w, "SID=u/abc\nAuth=u/abc\n")
		case strings.HasSuffix(r.URL.Path, "/reader/api/0/subscription/list"):
			authSeen = r.Header.Get("Authorization")
			_, _ = io.WriteString(w, `{"subscriptions":[{"id":"feed/42","title":"Xkcd","categories":[{"id":"user/-/label/Fun","label":"Fun"}]}]}`)
		default:
			http.NotFound(w, r)
		}
	}), nil)

	subs, err := c.Subscriptions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].ID != "feed/42" || subs[0].Categories[0].Label != "Fun" {
		t.Fatalf("unexpected subscriptions: %+v", subs)
	}
	if authSeen != "GoogleLogin auth=u/abc" {
		t.Fatalf("bad auth header %q", authSeen)
	}
}

func TestEditTagUsesIAndToken(t *testing.T) {
	var body string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/accounts/ClientLogin"):
			_, _ = io.WriteString(w, "Auth=u/tok\n")
		case strings.HasSuffix(r.URL.Path, "/reader/api/0/token"):
			_, _ = io.WriteString(w, "write-token\n")
		case strings.HasSuffix(r.URL.Path, "/reader/api/0/edit-tag"):
			raw, _ := io.ReadAll(r.Body)
			body = string(raw)
			_, _ = io.WriteString(w, "OK")
		default:
			http.NotFound(w, r)
		}
	}), nil)

	if err := c.MarkRead(context.Background(), []string{"tag:google.com,2005:reader/item/00000000000000ff"}, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "i=255") {
		t.Fatalf("item id missing from body: %s", body)
	}
	if strings.Contains(body, "s=") {
		t.Fatalf("body must not use s for ids: %s", body)
	}
	if !strings.Contains(body, "T=write-token") {
		t.Fatalf("token missing: %s", body)
	}
	if !strings.Contains(body, "a=user%2F-%2Fstate%2Fcom.google%2Fread") {
		t.Fatalf("read tag missing: %s", body)
	}
}

func TestReloginOn401(t *testing.T) {
	var logins int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/accounts/ClientLogin"):
			atomic.AddInt32(&logins, 1)
			_, _ = io.WriteString(w, "Auth=u/tok\n")
		case strings.HasSuffix(r.URL.Path, "/reader/api/0/unread-count"):
			if r.Header.Get("Authorization") == "GoogleLogin auth=u/tok" && atomic.LoadInt32(&logins) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"max":3,"unreadcounts":[{"id":"feed/1","count":3}]}`)
		default:
			http.NotFound(w, r)
		}
	}), nil)

	uc, err := c.UnreadCounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if uc.TotalUnread() != 3 {
		t.Fatalf("unexpected total %d", uc.TotalUnread())
	}
	if atomic.LoadInt32(&logins) != 2 {
		t.Fatalf("expected a second login, got %d", atomic.LoadInt32(&logins))
	}
}

func TestInvalidJSON(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/accounts/ClientLogin") {
			_, _ = io.WriteString(w, "Auth=u/tok\n")
			return
		}
		_, _ = io.WriteString(w, "<html>nope</html>")
	}), nil)
	if _, err := c.Subscriptions(context.Background()); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("expected ErrInvalidResponse, got %v", err)
	}
}

func TestResponseTooLarge(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/accounts/ClientLogin") {
			_, _ = io.WriteString(w, "Auth=u/tok\n")
			return
		}
		_, _ = io.WriteString(w, strings.Repeat("x", 4096))
	}), func(o *Options) { o.MaxResponseBytes = 1024 })
	if _, err := c.Subscriptions(context.Background()); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
}

func TestTimeout(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/accounts/ClientLogin") {
			_, _ = io.WriteString(w, "Auth=u/tok\n")
			return
		}
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, `{}`)
	}), func(o *Options) { o.Timeout = 50 * time.Millisecond })
	if _, err := c.Subscriptions(context.Background()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

func TestLoginRejectedDoesNotLeakPassword(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "bad password p")
	}), nil)
	_, err := c.Subscriptions(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if strings.Contains(err.Error(), "p") && strings.Contains(err.Error(), "bad password") {
		t.Fatalf("error leaks upstream body: %v", err)
	}
}

func TestParseItemKey(t *testing.T) {
	cases := map[string]string{
		"tag:google.com,2005:reader/item/00000000000000ff": "255",
		"tag:google.com,2005:reader/item/00065eab4fd67499": "1792939732137113",
		"1792939732137113":   "1792939732137113",
		"000000000000000255": "255",
	}
	for in, want := range cases {
		got, err := ParseItemKey(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != want {
			t.Fatalf("%s: got %s want %s", in, got, want)
		}
	}
	for _, bad := range []string{"", "abc", "tag:google.com,2005:reader/item/zz", "-1"} {
		if _, err := ParseItemKey(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}
