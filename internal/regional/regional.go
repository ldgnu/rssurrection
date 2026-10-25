// Package regional fetches public regional news feeds that do not belong to
// the user's FreshRSS account. Everything is read-only and cached in memory.
package regional

import (
	"context"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxBody      = 2 << 20
	cacheTTL     = 10 * time.Minute
	maxPerSource = 40
)

// Source is one public feed. Lang is "es" or "en".
type Source struct {
	Name string
	URL  string
	Lang string
}

// Item is a normalized article from a public feed.
type Item struct {
	Title     string
	Link      string
	Source    string
	Summary   string
	ImageURL  string
	Published time.Time
	Lang      string
}

// Fetcher downloads and merges the configured sources.
type Fetcher struct {
	sources []Source
	limit   int
	hc      *http.Client

	mu    sync.Mutex
	items []Item
	at    time.Time
}

// New builds a fetcher. limit caps the number of returned items.
func New(sources []Source, limit int) *Fetcher {
	if limit <= 0 {
		limit = 12
	}
	return &Fetcher{
		sources: sources,
		limit:   limit,
		hc: &http.Client{
			Timeout: 12 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        8,
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     60 * time.Second,
			},
		},
	}
}

// Configured reports whether there is at least one source.
func (f *Fetcher) Configured() bool { return f != nil && len(f.sources) > 0 }

// Sources returns the configured source names.
func (f *Fetcher) Sources() []string {
	if f == nil {
		return nil
	}
	out := make([]string, 0, len(f.sources))
	for _, s := range f.sources {
		out = append(out, s.Name)
	}
	return out
}

// Items returns the merged feed, cached for a few minutes.
func (f *Fetcher) Items(ctx context.Context) ([]Item, error) {
	if !f.Configured() {
		return nil, nil
	}
	f.mu.Lock()
	if time.Since(f.at) < cacheTTL && f.items != nil {
		out := f.items
		f.mu.Unlock()
		return out, nil
	}
	f.mu.Unlock()

	type res struct {
		items []Item
		err   error
	}
	ch := make(chan res, len(f.sources))
	for _, src := range f.sources {
		go func(src Source) {
			items, err := f.fetchOne(ctx, src)
			ch <- res{items, err}
		}(src)
	}

	var all []Item
	var errs []error
	for range f.sources {
		r := <-ch
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		all = append(all, r.items...)
	}
	if len(all) == 0 {
		return nil, errors.Join(errs...)
	}

	seen := make(map[string]bool, len(all))
	bySource := make(map[string][]Item, len(f.sources))
	var order []string
	for _, it := range all {
		if it.Link == "" || seen[it.Link] {
			continue
		}
		seen[it.Link] = true
		if _, ok := bySource[it.Source]; !ok {
			order = append(order, it.Source)
		}
		bySource[it.Source] = append(bySource[it.Source], it)
	}
	for _, src := range order {
		list := bySource[src]
		sort.SliceStable(list, func(i, j int) bool { return list[i].Published.After(list[j].Published) })
		bySource[src] = list
	}

	// Round-robin so a single prolific source cannot flood the section.
	uniq := make([]Item, 0, f.limit)
	for len(uniq) < f.limit {
		progressed := false
		for _, src := range order {
			list := bySource[src]
			if len(list) == 0 {
				continue
			}
			uniq = append(uniq, list[0])
			bySource[src] = list[1:]
			progressed = true
			if len(uniq) >= f.limit {
				break
			}
		}
		if !progressed {
			break
		}
	}

	f.mu.Lock()
	f.items = uniq
	f.at = time.Now()
	f.mu.Unlock()
	return uniq, nil
}

func (f *Fetcher) fetchOne(ctx context.Context, src Source) ([]Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "RSSurrection/0.1 (+https://github.com/ldgnu/rssurrection)")
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml;q=0.9, */*;q=0.5")

	resp, err := f.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("regional: upstream status")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	return parse(body, src), nil
}

type rssDoc struct {
	XMLName xml.Name
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
			PubDate     string `xml:"pubDate"`
			Enclosure   struct {
				URL  string `xml:"url,attr"`
				Type string `xml:"type,attr"`
			} `xml:"enclosure"`
			Media struct {
				URL  string `xml:"url,attr"`
				Type string `xml:"type,attr"`
			} `xml:"http://search.yahoo.com/mrss/ content"`
			Encoded string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
		} `xml:"item"`
	} `xml:"channel"`
	Entries []struct {
		Title string `xml:"title"`
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
		Summary   string `xml:"summary"`
		Content   string `xml:"content"`
		Published string `xml:"published"`
		Updated   string `xml:"updated"`
	} `xml:"entry"`
}

func parse(body []byte, src Source) []Item {
	var doc rssDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil
	}
	var out []Item

	for _, it := range doc.Channel.Items {
		if len(out) >= maxPerSource {
			break
		}
		link := strings.TrimSpace(it.Link)
		if link == "" {
			continue
		}
		item := Item{
			Title:     cleanText(it.Title),
			Link:      link,
			Source:    src.Name,
			Lang:      src.Lang,
			Summary:   excerpt(it.Description, 150),
			Published: parseDate(it.PubDate),
		}
		switch {
		case strings.HasPrefix(it.Enclosure.Type, "image/"):
			item.ImageURL = it.Enclosure.URL
		case it.Media.URL != "":
			item.ImageURL = it.Media.URL
		default:
			item.ImageURL = firstImage(it.Encoded, it.Description)
		}
		if item.Title == "" {
			continue
		}
		out = append(out, item)
	}

	for _, e := range doc.Entries {
		if len(out) >= maxPerSource {
			break
		}
		link := ""
		for _, l := range e.Links {
			if l.Rel == "" || l.Rel == "alternate" {
				link = l.Href
				break
			}
		}
		if link == "" {
			continue
		}
		date := e.Published
		if date == "" {
			date = e.Updated
		}
		item := Item{
			Title:     cleanText(e.Title),
			Link:      link,
			Source:    src.Name,
			Lang:      src.Lang,
			Summary:   excerpt(e.Summary, 150),
			Published: parseDate(date),
		}
		item.ImageURL = firstImage(e.Content, e.Summary)
		if item.Title == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

var (
	tagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	imgRe   = regexp.MustCompile(`(?is)<img[^>]+src=["']([^"']+)["']`)
	spaceRe = regexp.MustCompile(`\s+`)
)

func cleanText(s string) string {
	s = html.UnescapeString(tagRe.ReplaceAllString(s, " "))
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

func excerpt(s string, n int) string {
	t := cleanText(s)
	r := []rune(t)
	if len(r) <= n {
		return t
	}
	cut := n
	for cut > 0 && r[cut-1] != ' ' {
		cut--
	}
	if cut < n/2 {
		cut = n
	}
	return strings.TrimSpace(string(r[:cut])) + "…"
}

func firstImage(parts ...string) string {
	for _, p := range parts {
		if m := imgRe.FindStringSubmatch(p); len(m) == 2 {
			src := html.UnescapeString(strings.TrimSpace(m[1]))
			if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
				return src
			}
		}
	}
	return ""
}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822,
		time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05Z0700",
		"Mon, 2 Jan 2006 15:04:05 -0700", "2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
