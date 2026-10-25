package freshrss

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// StreamKind enumerates the FreshRSS streams RSSurrection reads.
type StreamKind int

const (
	StreamReadingList StreamKind = iota
	StreamStarred
	StreamFeed
	StreamLabel
)

// Stream identifies a stream without exposing raw path building to callers.
type Stream struct {
	kind StreamKind
	id   string
}

// ReadingList is the main "all feeds" stream.
func ReadingList() Stream { return Stream{kind: StreamReadingList} }

// Starred is the favourites stream.
func Starred() Stream { return Stream{kind: StreamStarred} }

const (
	maxFeedIDLen  = 20
	maxLabelLen   = 200
	maxStreamName = 250
)

// FeedStream builds a stream for a feed ID. It accepts "42" or "feed/42".
func FeedStream(ref string) (Stream, error) {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "feed/")
	if ref == "" || len(ref) > maxFeedIDLen || !isDigits(ref) {
		return Stream{}, fmt.Errorf("invalid feed id %q", ref)
	}
	return Stream{kind: StreamFeed, id: strings.TrimLeft(ref, "0")}, nil
}

// LabelStream builds a stream for a category/folder or tag name.
func LabelStream(name string) (Stream, error) {
	name = strings.TrimSpace(name)
	if err := validateLabel(name); err != nil {
		return Stream{}, err
	}
	return Stream{kind: StreamLabel, id: name}, nil
}

// ParseStream accepts a canonical Google Reader stream id.
func ParseStream(s string) (Stream, error) {
	s = strings.TrimSpace(s)
	switch s {
	case TagReadingL:
		return ReadingList(), nil
	case TagStarred:
		return Starred(), nil
	}
	if strings.HasPrefix(s, "feed/") {
		return FeedStream(s)
	}
	if strings.HasPrefix(s, "user/-/label/") {
		return LabelStream(strings.TrimPrefix(s, "user/-/label/"))
	}
	return Stream{}, fmt.Errorf("unsupported stream %q", s)
}

// ID returns the canonical Google Reader stream id.
func (s Stream) ID() string {
	switch s.kind {
	case StreamStarred:
		return TagStarred
	case StreamFeed:
		return "feed/" + s.id
	case StreamLabel:
		return "user/-/label/" + s.id
	default:
		return TagReadingL
	}
}

// Kind returns the stream kind.
func (s Stream) Kind() StreamKind { return s.kind }

// FeedID returns the numeric feed id for feed streams.
func (s Stream) FeedID() string { return s.id }

// Label returns the label/category name for label streams.
func (s Stream) Label() string { return s.id }

// Path returns the escaped path segment list used by /stream/contents.
func (s Stream) Path() string {
	switch s.kind {
	case StreamStarred:
		return "user/-/state/com.google/starred"
	case StreamFeed:
		return "feed/" + s.id
	case StreamLabel:
		return "user/-/label/" + url.PathEscape(s.id)
	default:
		return "user/-/state/com.google/reading-list"
	}
}

func validateLabel(name string) error {
	if name == "" {
		return errors.New("empty label")
	}
	if len(name) > maxLabelLen {
		return errors.New("label too long")
	}
	if strings.Contains(name, "/") {
		return errors.New(`label must not contain "/"`)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errors.New("label contains control characters")
		}
	}
	return nil
}

// TagID builds a user tag id (used when adding labels).
func TagID(label string) (string, error) {
	if err := validateLabel(label); err != nil {
		return "", err
	}
	return "user/-/label/" + label, nil
}

// LabelFromTagID extracts the label name from a user tag id.
func LabelFromTagID(id string) (string, bool) {
	if strings.HasPrefix(id, "user/-/label/") {
		return strings.TrimPrefix(id, "user/-/label/"), true
	}
	for _, p := range []string{"user/", "label/"} {
		_ = p
	}
	if i := strings.Index(id, "/label/"); i >= 0 {
		return id[i+len("/label/"):], true
	}
	return "", false
}
