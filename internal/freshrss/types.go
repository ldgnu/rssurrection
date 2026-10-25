package freshrss

import (
	"regexp"
	"strings"
	"time"
)

// State tags understood by FreshRSS.
const (
	TagRead      = "user/-/state/com.google/read"
	TagUnread    = "user/-/state/com.google/unread"
	TagStarred   = "user/-/state/com.google/starred"
	TagReadingL  = "user/-/state/com.google/reading-list"
	TagMain      = "user/-/state/org.freshrss/main"
	TagImportant = "user/-/state/org.freshrss/important"
)

// Subscription is one feed the user follows.
type Subscription struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	URL        string     `json:"url"`
	HTMLURL    string     `json:"htmlUrl"`
	IconURL    string     `json:"iconUrl"`
	Categories []Category `json:"categories"`
}

// Category is a folder (FreshRSS category) attached to a subscription.
type Category struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Tag is an entry from /tag/list. Folders carry Type "folder".
type Tag struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// UnreadEntry is a single line of /unread-count.
type UnreadEntry struct {
	ID                   string `json:"id"`
	Count                int    `json:"count"`
	NewestItemTimestamp  string `json:"newestItemTimestampUsec"`
	NewestItemTimestampT time.Time
}

// UnreadCounts is the /unread-count response.
type UnreadCounts struct {
	Max     int           `json:"max"`
	Entries []UnreadEntry `json:"unreadcounts"`
}

// TotalUnread sums feed entries only, so labels are not counted twice.
func (u UnreadCounts) TotalUnread() int {
	total := 0
	for _, e := range u.Entries {
		if strings.HasPrefix(e.ID, "feed/") {
			total += e.Count
		}
	}
	return total
}

// Link is a href object used by canonical/alternate.
type Link struct {
	Href string `json:"href"`
	Type string `json:"type"`
}

// Enclosure is media attached to an item.
type Enclosure struct {
	Href   string `json:"href"`
	Type   string `json:"type"`
	Length int64  `json:"length,omitempty"`
}

// Origin identifies the feed an item came from.
type Origin struct {
	StreamID string `json:"streamId"`
	Title    string `json:"title"`
	HTMLURL  string `json:"htmlUrl"`
	FeedURL  string `json:"feedUrl"`
}

// Summary holds the article body as provided by FreshRSS.
type Summary struct {
	Content string `json:"content"`
}

// Item is one article as returned by /stream/contents.
type Item struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	Author        string      `json:"author"`
	Published     Timestamp   `json:"published"`
	CrawlTimeMsec string      `json:"crawlTimeMsec"`
	TimestampUsec string      `json:"timestampUsec"`
	Canonical     []Link      `json:"canonical"`
	Alternate     []Link      `json:"alternate"`
	Categories    []string    `json:"categories"`
	Origin        Origin      `json:"origin"`
	Summary       Summary     `json:"summary"`
	Content       Summary     `json:"content"`
	Enclosure     []Enclosure `json:"enclosure"`
}

// Article is the normalized, presentation-ready shape used by the web layer.
type Article struct {
	Key         string
	Title       string
	Author      string
	Published   time.Time
	FeedID      string
	FeedTitle   string
	FeedHTMLURL string
	Link        string
	ContentHTML string
	ImageURL    string
	Categories  []string
	Read        bool
	Starred     bool
}

var imgSrcRe = regexp.MustCompile(`(?is)<img[^>]+src=["']([^"']+)["']`)

// Article normalizes an Item. When the item carries no usable identifier the
// returned Article has an empty Key and should be skipped by callers.
func (it Item) Article() Article {
	key, _ := ParseItemKey(it.ID)
	a := Article{
		Key:         key,
		Title:       strings.TrimSpace(it.Title),
		Author:      strings.TrimSpace(it.Author),
		Published:   it.published(),
		FeedID:      it.Origin.StreamID,
		FeedTitle:   it.Origin.Title,
		FeedHTMLURL: it.Origin.HTMLURL,
		Link:        it.link(),
		ContentHTML: it.bodyHTML(),
		Categories:  it.labels(),
	}
	for _, c := range it.Categories {
		switch c {
		case TagRead:
			a.Read = true
		case TagStarred:
			a.Starred = true
		}
	}
	a.ImageURL = it.imageURL()
	return a
}

func (it Item) published() time.Time {
	if !it.Published.IsZero() {
		return it.Published.Time
	}
	if t := parseUsec(it.TimestampUsec); !t.IsZero() {
		return t
	}
	if t := parseUsec(it.CrawlTimeMsec + "000"); !t.IsZero() {
		return t
	}
	return time.Time{}
}

func (it Item) link() string {
	for _, l := range it.Canonical {
		if l.Href != "" {
			return l.Href
		}
	}
	for _, l := range it.Alternate {
		if l.Href != "" {
			return l.Href
		}
	}
	return ""
}

func (it Item) bodyHTML() string {
	if s := strings.TrimSpace(it.Summary.Content); s != "" {
		return s
	}
	return strings.TrimSpace(it.Content.Content)
}

func (it Item) labels() []string {
	out := make([]string, 0, len(it.Categories))
	for _, c := range it.Categories {
		if strings.HasPrefix(c, "user/-/label/") {
			out = append(out, strings.TrimPrefix(c, "user/-/label/"))
			continue
		}
		// Plain tags (no namespace) are labels too, but skip feed/user states.
		if !strings.Contains(c, "/") {
			out = append(out, c)
		}
	}
	return out
}

func (it Item) imageURL() string {
	for _, e := range it.Enclosure {
		if strings.HasPrefix(strings.ToLower(e.Type), "image/") && e.Href != "" {
			return e.Href
		}
	}
	if m := imgSrcRe.FindStringSubmatch(it.bodyHTML()); len(m) == 2 {
		src := strings.TrimSpace(m[1])
		if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
			return src
		}
	}
	return ""
}

// StreamPage is a page of /stream/contents.
type StreamPage struct {
	ID           string `json:"id"`
	Updated      int64  `json:"updated"`
	Items        []Item `json:"items"`
	Continuation string `json:"continuation"`
}

// IDPage is a page of /stream/items/ids.
type IDPage struct {
	ItemRefs     []ItemRef `json:"itemRefs"`
	Continuation string    `json:"continuation"`
}

// ItemRef is one entry of /stream/items/ids.
type ItemRef struct {
	ID string `json:"id"`
}

// Keys returns the normalized decimal item keys of the page.
func (p IDPage) Keys() []string {
	keys := make([]string, 0, len(p.ItemRefs))
	for _, r := range p.ItemRefs {
		if k, err := ParseItemKey(r.ID); err == nil && k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

// Diag is the result of a connectivity self-check.
type Diag struct {
	BaseURL   string
	User      string
	CheckedAt time.Time
	Feeds     int
	Folders   int
	Unread    int
	Steps     []Step
}

// Step is one stage of the self-check.
type Step struct {
	Name      string
	OK        bool
	Duration  time.Duration
	Detail    string
	Err       string
	ErrorKind string
}

// OK reports whether every step succeeded.
func (d Diag) OK() bool {
	for _, s := range d.Steps {
		if !s.OK {
			return false
		}
	}
	return len(d.Steps) > 0
}
