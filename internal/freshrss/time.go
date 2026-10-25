package freshrss

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Timestamp decodes the several date shapes seen across FreshRSS versions and
// Google Reader compatible servers: epoch seconds, epoch milliseconds, numeric
// strings, RFC3339 and "YYYY-MM-DD HH:MM:SS".
type Timestamp struct{ time.Time }

// UnmarshalJSON implements json.Unmarshaler.
func (t *Timestamp) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		t.Time = time.Time{}
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		v, err := parseTimeString(s)
		if err != nil {
			return err
		}
		t.Time = v
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("unsupported timestamp %s", b)
	}
	t.Time = fromEpoch(f)
	return nil
}

func parseTimeString(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return fromEpoch(f), nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"Mon, 02 Jan 2006 15:04:05 MST",
	} {
		if v, err := time.Parse(layout, s); err == nil {
			return v.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported date %q", s)
}

func fromEpoch(f float64) time.Time {
	switch {
	case f <= 0:
		return time.Time{}
	case f > 1e15: // microseconds
		return time.UnixMicro(int64(f)).UTC()
	case f > 1e12: // milliseconds
		return time.UnixMilli(int64(f)).UTC()
	case f > 1e9*100: // seconds but absurdly large: treat as milliseconds
		return time.UnixMilli(int64(f)).UTC()
	default:
		return time.Unix(int64(f), 0).UTC()
	}
}

// parseUsec parses FreshRSS' timestampUsec (microseconds as a string).
func parseUsec(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		if v, err2 := parseTimeString(s); err2 == nil {
			return v
		}
		return time.Time{}
	}
	if n <= 0 {
		return time.Time{}
	}
	return time.UnixMicro(n).UTC()
}
