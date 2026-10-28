package freshrss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	authTTL  = 6 * time.Hour
	tokenTTL = 10 * time.Minute

	defaultTimeout    = 10 * time.Second
	defaultMaxBytes   = 8 << 20
	defaultUserAgent  = "RSSurrection (+https://github.com/ldgnu/rssurrection)"
	maxErrorBodyBytes = 256
	maxCount          = 200
)

// Options configures a Client. Only BaseURL, User and Password are required.
type Options struct {
	BaseURL          *url.URL
	User             string
	Password         string
	Timeout          time.Duration
	MaxResponseBytes int64
	HTTPClient       *http.Client
	UserAgent        string
	Now              func() time.Time
}

// Client talks to the FreshRSS Google Reader compatible API.
//
// It is safe for concurrent use. Tokens are refreshed in memory only; nothing
// is ever written to disk.
type Client struct {
	base    *url.URL
	user    string
	pass    string
	hc      *http.Client
	maxBody int64
	ua      string
	now     func() time.Time

	authMu  sync.Mutex // protege auth/token; nunca se retiene durante I/O
	loginMu sync.Mutex // serializa los logins
	auth    string
	authAt  time.Time
	token   string
	tokenAt time.Time
}

// New validates the options and returns a ready client. It performs no
// network I/O: call Login or any read method to authenticate.
func New(opts Options) (*Client, error) {
	if opts.BaseURL == nil {
		return nil, errors.New("freshrss: missing base URL")
	}
	if opts.BaseURL.Host == "" {
		return nil, errors.New("freshrss: base URL without host")
	}
	if strings.TrimSpace(opts.User) == "" {
		return nil, errors.New("freshrss: missing user")
	}
	if opts.Password == "" {
		return nil, errors.New("freshrss: missing API password")
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxBody := opts.MaxResponseBytes
	if maxBody <= 0 {
		maxBody = defaultMaxBytes
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	hc := opts.HTTPClient
	if hc == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.MaxIdleConns = 16
		tr.MaxIdleConnsPerHost = 4
		tr.MaxConnsPerHost = 8
		tr.IdleConnTimeout = 90 * time.Second
		tr.TLSHandshakeTimeout = timeout
		tr.ResponseHeaderTimeout = timeout
		tr.ForceAttemptHTTP2 = true
		hc = &http.Client{Transport: tr, Timeout: 0} // per-request context carries the timeout
	}

	return &Client{
		base:    opts.BaseURL,
		user:    opts.User,
		pass:    opts.Password,
		hc:      hc,
		maxBody: maxBody,
		ua:      ua,
		now:     now,
	}, nil
}

// BaseURL returns the endpoint this client talks to (safe to display).
func (c *Client) BaseURL() string { return c.base.String() }

// User returns the configured user name (safe to display).
func (c *Client) User() string { return c.user }

// CloseIdleConnections releases pooled connections.
func (c *Client) CloseIdleConnections() {
	if tr, ok := c.hc.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}

// Login forces a fresh authentication.
func (c *Client) Login(ctx context.Context) error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	_, err := c.loginLocked(ctx)
	return err
}

// loginLocked must be called with loginMu held. It never holds authMu while
// doing network I/O, so error paths cannot deadlock.
func (c *Client) loginLocked(ctx context.Context) (string, error) {
	form := url.Values{}
	form.Set("Email", c.user)
	form.Set("Passwd", c.pass)
	body, err := c.do(ctx, "login", http.MethodPost, "/accounts/ClientLogin", nil, form, "")
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && (ae.Status == 401 || ae.Status == 403) {
			return "", apiErr("login", ae.Status, ErrUnauthorized,
				"FreshRSS rejected the API credentials (check FRESHRSS_USER and FRESHRSS_API_PASSWORD)")
		}
		return "", err
	}
	var auth, sid string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "Auth="); ok {
			auth = v
		} else if v, ok := strings.CutPrefix(line, "SID="); ok {
			sid = v
		}
	}
	if auth == "" {
		return "", apiErr("login", 0, ErrInvalidResponse, "FreshRSS login response did not contain an Auth token")
	}
	if sid == "" {
		sid = auth
	}
	c.authMu.Lock()
	c.auth = auth
	c.authAt = c.now()
	c.token = ""
	c.tokenAt = time.Time{}
	c.authMu.Unlock()
	return auth, nil
}

func (c *Client) authToken(ctx context.Context) (string, error) {
	c.authMu.Lock()
	if c.auth != "" && c.now().Sub(c.authAt) < authTTL {
		tok := c.auth
		c.authMu.Unlock()
		return tok, nil
	}
	c.authMu.Unlock()

	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	// Otro goroutine pudo haber renovado el token mientras esperábamos.
	c.authMu.Lock()
	if c.auth != "" && c.now().Sub(c.authAt) < authTTL {
		tok := c.auth
		c.authMu.Unlock()
		return tok, nil
	}
	c.authMu.Unlock()
	return c.loginLocked(ctx)
}

func (c *Client) invalidateAuth() {
	c.authMu.Lock()
	c.auth = ""
	c.authAt = time.Time{}
	c.token = ""
	c.tokenAt = time.Time{}
	c.authMu.Unlock()
}

func (c *Client) writeToken(ctx context.Context) (string, error) {
	auth, err := c.authToken(ctx)
	if err != nil {
		return "", err
	}
	c.authMu.Lock()
	if c.token != "" && c.now().Sub(c.tokenAt) < tokenTTL {
		tok := c.token
		c.authMu.Unlock()
		return tok, nil
	}
	c.authMu.Unlock()

	body, err := c.do(ctx, "token", http.MethodGet, "/reader/api/0/token", nil, nil, auth)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(body))
	if tok == "" {
		return "", apiErr("token", 0, ErrInvalidResponse, "FreshRSS returned an empty write token")
	}
	c.authMu.Lock()
	c.token = tok
	c.tokenAt = c.now()
	c.authMu.Unlock()
	return tok, nil
}

func (c *Client) target(path string, q url.Values) string {
	base := strings.TrimRight(c.base.String(), "/")
	s := base + path
	if len(q) > 0 {
		s += "?" + q.Encode()
	}
	return s
}

func (c *Client) do(ctx context.Context, op string, method, path string, q, form url.Values, auth string) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.target(path, q), body)
	if err != nil {
		return nil, apiErr(op, 0, ErrBadRequest, "cannot build request")
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if auth != "" {
		req.Header.Set("Authorization", "GoogleLogin auth="+auth)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, c.transportError(op, err)
	}
	defer func() { _ = resp.Body.Close() }()

	limited := io.LimitReader(resp.Body, c.maxBody+1)
	payload, readErr := io.ReadAll(limited)
	if readErr != nil {
		return nil, c.transportError(op, readErr)
	}
	if int64(len(payload)) > c.maxBody {
		return nil, apiErr(op, resp.StatusCode, ErrTooLarge,
			fmt.Sprintf("response exceeded the %d byte limit", c.maxBody))
	}

	if resp.StatusCode >= 400 {
		return nil, c.statusError(op, resp.StatusCode, payload)
	}
	return payload, nil
}

func (c *Client) transportError(op string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &APIError{Op: op, Kind: ErrTimeout, Message: "request cancelled or timed out"}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return &APIError{Op: op, Kind: ErrTimeout, Message: "request timed out"}
	}
	return &APIError{Op: op, Kind: ErrTransport, Message: "cannot reach FreshRSS (network or TLS error)"}
}

func (c *Client) statusError(op string, status int, payload []byte) error {
	detail := sanitizeSnippet(payload)
	var kind error
	switch {
	case status == 401 || status == 403:
		kind = ErrUnauthorized
		detail = "FreshRSS rejected the authentication"
	case status == 404:
		kind = ErrNotFound
	case status == 429:
		kind = ErrUpstream
		detail = "FreshRSS is rate limiting requests"
	case status >= 500:
		kind = ErrUpstream
	case status >= 400:
		kind = ErrBadRequest
	}
	msg := fmt.Sprintf("unexpected HTTP status %d", status)
	if detail != "" {
		msg += ": " + detail
	}
	return apiErr(op, status, kind, c.redact(msg))
}

func (c *Client) redact(s string) string {
	if c.pass != "" {
		s = strings.ReplaceAll(s, c.pass, "REDACTED")
	}
	c.authMu.Lock()
	auth, token := c.auth, c.token
	c.authMu.Unlock()
	if auth != "" {
		s = strings.ReplaceAll(s, auth, "REDACTED")
	}
	if token != "" {
		s = strings.ReplaceAll(s, token, "REDACTED")
	}
	return s
}

func sanitizeSnippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > maxErrorBodyBytes {
		s = s[:maxErrorBodyBytes] + "…"
	}
	return s
}

func (c *Client) get(ctx context.Context, op, path string, q url.Values, out any) error {
	for attempt := 0; ; attempt++ {
		auth, err := c.authToken(ctx)
		if err != nil {
			return err
		}
		body, err := c.do(ctx, op, http.MethodGet, path, q, nil, auth)
		if err != nil {
			if attempt == 0 && isAuthFailure(err) {
				c.invalidateAuth()
				continue
			}
			return err
		}
		return decode(op, body, out)
	}
}

func (c *Client) postWrite(ctx context.Context, op, path string, form url.Values) error {
	for attempt := 0; ; attempt++ {
		auth, err := c.authToken(ctx)
		if err != nil {
			return err
		}
		token, err := c.writeToken(ctx)
		if err != nil {
			return err
		}
		form.Set("T", token)
		_, err = c.do(ctx, op, http.MethodPost, path, nil, form, auth)
		if err != nil {
			if attempt == 0 && isAuthFailure(err) {
				c.invalidateAuth()
				continue
			}
			return err
		}
		return nil
	}
}

func isAuthFailure(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == 401 || ae.Status == 403)
}

func decode(op string, body []byte, out any) error {
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return apiErr(op, 0, ErrInvalidResponse,
			fmt.Sprintf("response is not valid JSON (%v)", err))
	}
	return nil
}

// Subscriptions lists every feed with its categories.
func (c *Client) Subscriptions(ctx context.Context) ([]Subscription, error) {
	var out struct {
		Subscriptions []Subscription `json:"subscriptions"`
	}
	q := url.Values{"output": {"json"}}
	if err := c.get(ctx, "subscriptions", "/reader/api/0/subscription/list", q, &out); err != nil {
		return nil, err
	}
	return out.Subscriptions, nil
}

// Tags lists folders and tags.
func (c *Client) Tags(ctx context.Context) ([]Tag, error) {
	var out struct {
		Tags []Tag `json:"tags"`
	}
	q := url.Values{"output": {"json"}}
	if err := c.get(ctx, "tags", "/reader/api/0/tag/list", q, &out); err != nil {
		return nil, err
	}
	return out.Tags, nil
}

// UnreadCounts returns unread counters per feed, category and total.
func (c *Client) UnreadCounts(ctx context.Context) (UnreadCounts, error) {
	var out UnreadCounts
	q := url.Values{"output": {"json"}}
	if err := c.get(ctx, "unread-count", "/reader/api/0/unread-count", q, &out); err != nil {
		return UnreadCounts{}, err
	}
	return out, nil
}

// UnreadTotal is a shortcut for the sum of unread counters.
func (c *Client) UnreadTotal(ctx context.Context) (int, error) {
	u, err := c.UnreadCounts(ctx)
	if err != nil {
		return 0, err
	}
	return u.TotalUnread(), nil
}

// StreamQuery describes a /stream/contents request.
type StreamQuery struct {
	Stream         Stream
	Count          int
	Continuation   string
	Since          time.Time // ot: newer than (also matches recently modified on FreshRSS >= 1.29)
	Until          time.Time // nt: older than
	OnlyUnread     bool
	OnlyStarred    bool
	ExcludeStarred bool
	OldestFirst    bool
}

func (q StreamQuery) values() url.Values {
	v := url.Values{"output": {"json"}}
	v.Set("n", strconv.Itoa(clampCount(q.Count)))
	if q.Continuation != "" {
		v.Set("c", q.Continuation)
	}
	if !q.Since.IsZero() {
		v.Set("ot", strconv.FormatInt(q.Since.Unix(), 10))
	}
	if !q.Until.IsZero() {
		v.Set("nt", strconv.FormatInt(q.Until.Unix(), 10))
	}
	if q.OnlyUnread {
		v.Set("xt", TagRead)
	}
	if q.OnlyStarred {
		v.Set("it", TagStarred)
	}
	if q.ExcludeStarred {
		v.Add("xt", TagStarred)
	}
	if q.OldestFirst {
		v.Set("r", "o")
	}
	return v
}

func clampCount(n int) int {
	if n <= 0 {
		return 20
	}
	if n > maxCount {
		return maxCount
	}
	return n
}

// StreamContents fetches a page of articles.
func (c *Client) StreamContents(ctx context.Context, q StreamQuery) (StreamPage, error) {
	var page StreamPage
	path := "/reader/api/0/stream/contents/" + q.Stream.Path()
	if err := c.get(ctx, "stream/contents", path, q.values(), &page); err != nil {
		return StreamPage{}, err
	}
	return page, nil
}

// IDQuery describes a /stream/items/ids request.
type IDQuery struct {
	Stream       Stream
	Count        int
	Continuation string
	Since        time.Time
	Until        time.Time
	OnlyUnread   bool
	OldestFirst  bool
}

// ItemIDs returns a cheap page of item IDs (no article bodies).
func (c *Client) ItemIDs(ctx context.Context, q IDQuery) (IDPage, error) {
	v := url.Values{"output": {"json"}}
	v.Set("s", q.Stream.ID())
	v.Set("n", strconv.Itoa(clampCount(q.Count)))
	if q.Continuation != "" {
		v.Set("c", q.Continuation)
	}
	if !q.Since.IsZero() {
		v.Set("ot", strconv.FormatInt(q.Since.Unix(), 10))
	}
	if !q.Until.IsZero() {
		v.Set("nt", strconv.FormatInt(q.Until.Unix(), 10))
	}
	if q.OnlyUnread {
		v.Set("xt", TagRead)
	}
	if q.OldestFirst {
		v.Set("r", "o")
	}
	var page IDPage
	if err := c.get(ctx, "stream/items/ids", "/reader/api/0/stream/items/ids", v, &page); err != nil {
		return IDPage{}, err
	}
	return page, nil
}

// Items fetches full items for the given keys (decimal or GReader ids).
func (c *Client) Items(ctx context.Context, keys []string) ([]Article, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	if len(keys) > maxCount {
		return nil, apiErr("stream/items/contents", 0, ErrBadRequest,
			fmt.Sprintf("at most %d items per request", maxCount))
	}
	form := url.Values{}
	for _, k := range keys {
		id, err := FormatItemKey(strings.TrimSpace(k))
		if err != nil {
			if _, err2 := ParseItemKey(k); err2 != nil {
				return nil, apiErr("stream/items/contents", 0, ErrBadRequest, "invalid item id")
			}
			id, _ = FormatItemKey(mustKey(k))
		}
		form.Add("i", id)
	}

	auth, err := c.authToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.do(ctx, "stream/items/contents", http.MethodPost,
		"/reader/api/0/stream/items/contents", nil, form, auth)
	if err != nil {
		return nil, err
	}
	var page StreamPage
	if err := decode("stream/items/contents", body, &page); err != nil {
		return nil, err
	}
	out := make([]Article, 0, len(page.Items))
	for _, it := range page.Items {
		if a := it.Article(); a.Key != "" {
			out = append(out, a)
		}
	}
	return out, nil
}

func mustKey(s string) string {
	k, _ := ParseItemKey(s)
	return k
}

// EditTag adds and/or removes state tags on items.
func (c *Client) EditTag(ctx context.Context, keys, add, remove []string) error {
	if len(keys) == 0 {
		return nil
	}
	if len(keys) > maxCount {
		return apiErr("edit-tag", 0, ErrBadRequest, fmt.Sprintf("at most %d items per request", maxCount))
	}
	form := url.Values{}
	for _, k := range keys {
		id, err := ParseItemKey(k)
		if err != nil {
			return apiErr("edit-tag", 0, ErrBadRequest, "invalid item id")
		}
		form.Add("i", id) // FreshRSS reads ids from "i", never from "s"
	}
	for _, t := range add {
		if t == "" {
			continue
		}
		form.Add("a", t)
	}
	for _, t := range remove {
		if t == "" {
			continue
		}
		form.Add("r", t)
	}
	return c.postWrite(ctx, "edit-tag", "/reader/api/0/edit-tag", form)
}

// MarkRead marks items as read (or unread when read is false).
func (c *Client) MarkRead(ctx context.Context, keys []string, read bool) error {
	if read {
		return c.EditTag(ctx, keys, []string{TagRead}, nil)
	}
	return c.EditTag(ctx, keys, nil, []string{TagRead})
}

// SetStarred adds or removes the favourites flag.
func (c *Client) SetStarred(ctx context.Context, keys []string, starred bool) error {
	if starred {
		return c.EditTag(ctx, keys, []string{TagStarred}, nil)
	}
	return c.EditTag(ctx, keys, nil, []string{TagStarred})
}

// RenameFeed changes a subscription title (GReader subscription/edit).
func (c *Client) RenameFeed(ctx context.Context, feedID, title string) error {
	if _, err := FeedStream(feedID); err != nil {
		return apiErr("subscription/edit", 0, ErrBadRequest, "invalid feed id")
	}
	title = strings.TrimSpace(title)
	if title == "" || len([]rune(title)) > 200 {
		return apiErr("subscription/edit", 0, ErrBadRequest, "invalid title")
	}
	form := url.Values{}
	form.Set("ac", "edit")
	form.Set("s", "feed/"+strings.TrimLeft(strings.TrimPrefix(feedID, "feed/"), "0"))
	form.Set("t", title)
	return c.postWrite(ctx, "subscription/edit", "/reader/api/0/subscription/edit", form)
}

// Diag runs a sequential, read-only self check of the integration.
func (c *Client) Diag(ctx context.Context) (Diag, error) {
	d := Diag{BaseURL: c.base.String(), User: c.user, CheckedAt: c.now()}
	run := func(name string, fn func(context.Context) (string, error)) error {
		start := time.Now()
		detail, err := fn(ctx)
		step := Step{Name: name, OK: err == nil, Duration: time.Since(start), Detail: detail}
		if err != nil {
			step.Err = c.redact(err.Error())
			step.ErrorKind = Kind(err)
		}
		d.Steps = append(d.Steps, step)
		return err
	}

	if err := run("autenticación", func(ctx context.Context) (string, error) {
		if err := c.Login(ctx); err != nil {
			return "", err
		}
		return "token obtenido", nil
	}); err != nil {
		return d, err
	}

	if err := run("suscripciones", func(ctx context.Context) (string, error) {
		subs, err := c.Subscriptions(ctx)
		if err != nil {
			return "", err
		}
		d.Feeds = len(subs)
		return fmt.Sprintf("%d feeds", len(subs)), nil
	}); err != nil {
		return d, err
	}

	if err := run("categorías", func(ctx context.Context) (string, error) {
		tags, err := c.Tags(ctx)
		if err != nil {
			return "", err
		}
		for _, t := range tags {
			if t.Type == "folder" {
				d.Folders++
			}
		}
		return fmt.Sprintf("%d carpetas, %d etiquetas", d.Folders, len(tags)), nil
	}); err != nil {
		return d, err
	}

	if err := run("no leídos", func(ctx context.Context) (string, error) {
		u, err := c.UnreadCounts(ctx)
		if err != nil {
			return "", err
		}
		d.Unread = u.TotalUnread()
		return fmt.Sprintf("%d sin leer", d.Unread), nil
	}); err != nil {
		return d, err
	}

	if err := run("artículos", func(ctx context.Context) (string, error) {
		page, err := c.StreamContents(ctx, StreamQuery{Stream: ReadingList(), Count: 1})
		if err != nil {
			return "", err
		}
		if len(page.Items) == 0 {
			return "sin artículos todavía", nil
		}
		a := page.Items[0].Article()
		return fmt.Sprintf("último: %.60s", a.Title), nil
	}); err != nil {
		return d, err
	}

	return d, nil
}
