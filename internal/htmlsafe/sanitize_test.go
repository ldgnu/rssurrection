package htmlsafe

import (
	"strings"
	"testing"
)

func TestFragmentRemovesScriptsAndHandlers(t *testing.T) {
	in := `<p onclick="steal()">hola <script>alert(1)</script><img src="https://x.test/a.png" onerror="x()"></p>`
	out := Fragment(in)
	for _, bad := range []string{"script", "onclick", "onerror", "alert(1)"} {
		if strings.Contains(strings.ToLower(out), bad) {
			t.Fatalf("output still contains %q: %s", bad, out)
		}
	}
	if !strings.Contains(out, `<img src="https://x.test/a.png">`) {
		t.Fatalf("image was not preserved: %s", out)
	}
}

func TestFragmentBlocksJavascriptURLs(t *testing.T) {
	out := Fragment(`<a href="javascript:alert(1)">x</a><a href="https://ok.test">ok</a>`)
	if strings.Contains(strings.ToLower(out), "javascript:") {
		t.Fatalf("javascript URL survived: %s", out)
	}
	if !strings.Contains(out, `href="https://ok.test"`) {
		t.Fatalf("safe link was dropped: %s", out)
	}
	if !strings.Contains(out, `rel="noopener noreferrer nofollow"`) {
		t.Fatalf("link rel missing: %s", out)
	}
}

func TestFragmentUnwrapsUnknownTags(t *testing.T) {
	out := Fragment(`<div class="x"><custom-tag>texto</custom-tag></div>`)
	if !strings.Contains(out, "texto") {
		t.Fatalf("text lost: %s", out)
	}
	if strings.Contains(out, "custom-tag") || strings.Contains(out, "class") {
		t.Fatalf("unknown tag or attribute survived: %s", out)
	}
}

func TestFragmentDropsIframeAndForm(t *testing.T) {
	out := Fragment(`<iframe src="https://evil.test"></iframe><form action="/x"><input name="p"></form><p>ok</p>`)
	if strings.Contains(strings.ToLower(out), "iframe") || strings.Contains(strings.ToLower(out), "form") {
		t.Fatalf("dangerous element survived: %s", out)
	}
	if !strings.Contains(out, "<p>ok</p>") {
		t.Fatalf("safe content lost: %s", out)
	}
}

func TestExcerpt(t *testing.T) {
	got := Excerpt("<p>Uno dos <strong>tres</strong> cuatro</p>", 10)
	if got != "Uno dos…" {
		t.Fatalf("unexpected excerpt %q", got)
	}
}

func TestText(t *testing.T) {
	if got := Text("<p>a<br>b</p>"); got != "a b" {
		t.Fatalf("got %q", got)
	}
}
