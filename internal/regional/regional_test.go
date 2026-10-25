package regional

import (
	"strings"
	"testing"
)

const sampleRSS = `<?xml version="1.0"?><rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/">
<channel><title>t</title>
<item><title>Uno &amp; dos</title><link>https://example.com/1</link>
<description>&lt;p&gt;Resumen &lt;b&gt;con&lt;/b&gt; html&lt;/p&gt;</description>
<pubDate>Mon, 20 Oct 2026 10:00:00 -0300</pubDate></item>
<item><title>Dos</title><link>https://example.com/2</link>
<content:encoded><![CDATA[<p>texto <img src="https://example.com/a.jpg"></p>]]></content:encoded>
<pubDate>Mon, 20 Oct 2026 11:00:00 -0300</pubDate></item>
</channel></rss>`

func TestParseRSS(t *testing.T) {
	items := parse([]byte(sampleRSS), Source{Name: "Test"})
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Title != "Uno & dos" {
		t.Fatalf("html entities not decoded: %q", items[0].Title)
	}
	if strings.Contains(items[0].Summary, "<") {
		t.Fatalf("summary still has html: %q", items[0].Summary)
	}
	if items[1].ImageURL != "https://example.com/a.jpg" {
		t.Fatalf("image not extracted: %q", items[1].ImageURL)
	}
	if items[0].Published.IsZero() {
		t.Fatal("date not parsed")
	}
}

func TestParseRejectsBrokenXML(t *testing.T) {
	if items := parse([]byte("<html><body>nope"), Source{}); len(items) != 0 {
		t.Fatalf("expected no items, got %d", len(items))
	}
}

func TestExcerpt(t *testing.T) {
	got := excerpt("<p>uno dos tres cuatro</p>", 10)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis, got %q", got)
	}
}
