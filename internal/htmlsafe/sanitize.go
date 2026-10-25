// Package htmlsafe turns untrusted feed HTML into a small, safe subset.
//
// It is an allowlist: unknown elements are unwrapped (their text survives),
// dangerous elements are removed with their content, and every attribute that
// is not explicitly allowed is dropped.
package htmlsafe

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// allowed maps tag -> allowed attributes.
var allowed = map[string]map[string]bool{
	"p":          {},
	"br":         {},
	"hr":         {},
	"strong":     {},
	"b":          {},
	"em":         {},
	"i":          {},
	"u":          {},
	"s":          {},
	"small":      {},
	"sub":        {},
	"sup":        {},
	"mark":       {},
	"h1":         {},
	"h2":         {},
	"h3":         {},
	"h4":         {},
	"h5":         {},
	"h6":         {},
	"ul":         {},
	"ol":         {"start": true},
	"li":         {},
	"blockquote": {},
	"cite":       {},
	"pre":        {},
	"code":       {},
	"figure":     {},
	"figcaption": {},
	"a":          {"href": true},
	"img":        {"src": true, "alt": true, "width": true, "height": true},
}

// dropped elements are removed together with their subtree.
var dropped = map[string]bool{
	"script": true, "style": true, "iframe": true, "frame": true, "frameset": true,
	"object": true, "embed": true, "applet": true, "form": true, "input": true,
	"button": true, "textarea": true, "select": true, "option": true, "label": true,
	"link": true, "meta": true, "base": true, "svg": true, "math": true,
	"video": true, "audio": true, "source": true, "track": true, "canvas": true,
	"template": true, "noscript": true, "dialog": true, "portal": true,
}

// Fragment sanitizes an HTML fragment and returns it ready to embed.
func Fragment(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	ctx := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := html.ParseFragment(strings.NewReader(input), ctx)
	if err != nil {
		return escapeText(input)
	}
	var b strings.Builder
	for _, n := range nodes {
		walk(n, &b)
	}
	return strings.TrimSpace(b.String())
}

func walk(n *html.Node, b *strings.Builder) {
	switch n.Type {
	case html.TextNode:
		b.WriteString(html.EscapeString(n.Data))
		return
	case html.ElementNode:
		name := strings.ToLower(n.Data)
		if dropped[name] {
			return
		}
		attrs, ok := allowed[n.DataAtom.String()]
		if !ok {
			if _, known := allowed[name]; !known {
				// Unknown but harmless (e.g. <div>, <span>, <section>): unwrap.
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c, b)
				}
				return
			}
			attrs = allowed[name]
		}
		b.WriteString("<" + n.Data)
		for _, a := range n.Attr {
			key := strings.ToLower(a.Key)
			if !attrs[key] && !(key == "class" && false) {
				continue
			}
			val := strings.TrimSpace(a.Val)
			switch key {
			case "href":
				if !safeURL(val, false) {
					continue
				}
			case "src":
				if !safeURL(val, true) {
					continue
				}
			}
			b.WriteString(" " + key + `="` + html.EscapeString(val) + `"`)
		}
		if name == "a" {
			// Links always open in a new tab, without leaking the referrer
			// nor the opener.
			if hasAttr(n, "href") {
				b.WriteString(` target="_blank" rel="noopener noreferrer nofollow"`)
			}
		}
		b.WriteString(">")
		if name == "img" {
			return // void element
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, b)
		}
		b.WriteString("</" + n.Data + ">")
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, b)
	}
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return true
		}
	}
	return false
}

var schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// safeURL allows relative URLs, http(s), mailto and (optionally) inline images.
func safeURL(raw string, allowDataImage bool) bool {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\x00", "")
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "//") {
		return true // protocol-relative, still http(s)
	}
	m := schemeRe.FindString(lower)
	if m == "" {
		return true // relative path or fragment
	}
	switch m {
	case "http:", "https:", "mailto:":
		return true
	case "data:":
		return allowDataImage && strings.HasPrefix(lower, "data:image/")
	default:
		return false
	}
}

// Text returns the plain text of an HTML fragment with collapsed whitespace.
func Text(input string) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}
	ctx := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := html.ParseFragment(strings.NewReader(input), ctx)
	if err != nil {
		return strings.TrimSpace(input)
	}
	var b strings.Builder
	for _, n := range nodes {
		collectText(n, &b)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func collectText(n *html.Node, b *strings.Builder) {
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
		b.WriteString(" ")
	}
	if n.Type == html.ElementNode && dropped[strings.ToLower(n.Data)] {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectText(c, b)
	}
}

// Excerpt returns at most n runes of plain text, adding an ellipsis.
func Excerpt(input string, n int) string {
	t := Text(input)
	if n <= 0 || len([]rune(t)) <= n {
		return t
	}
	r := []rune(t)
	cut := n
	for cut > 0 && r[cut-1] != ' ' {
		cut--
	}
	if cut < n/2 {
		cut = n
	}
	return strings.TrimSpace(string(r[:cut])) + "…"
}

func escapeText(s string) string { return html.EscapeString(s) }
