package web

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ldgnu/rssurrection/internal/freshrss"
	"github.com/ldgnu/rssurrection/internal/htmlsafe"
	"github.com/ldgnu/rssurrection/internal/regional"
)

const (
	homePageSize      = 30
	feedBatchSize     = 30
	tilesPerFlip      = 7
	excerptRunes      = 180
	maxArticleKeySize = 32
	catsTTL           = 10 * time.Minute
)

type articleView struct {
	Key         string
	Title       string
	Feed        string
	Published   time.Time
	Excerpt     string
	ImageURL    string
	Link        string
	Domain      string
	Read        bool
	Starred     bool
	AccentClass string // tile-a0 … tile-a7, used when there is no image
	OGImage     string // lazy og:image fallback, resolved by the server
	External    bool   // opens the original site, not the reader
	Href        string
	Priority    bool   // first cards load eagerly
	Lang        string // "en" when the source needs translation
}

type categoryView struct {
	Label string
	Slug  string
}

func (s *Server) imageURL(raw string) string {
	if s.media == nil || raw == "" {
		return ""
	}
	return s.media.ImageURL(raw)
}

func (s *Server) ogURL(raw string) string {
	if s.media == nil || raw == "" {
		return ""
	}
	return s.media.OpenGraphURL(raw)
}

func (s *Server) buildArticles(items []freshrss.Item) []articleView {
	out := make([]articleView, 0, len(items))
	for _, it := range items {
		a := it.Article()
		if a.Key == "" {
			continue
		}
		v := articleView{
			Key:         a.Key,
			Title:       a.Title,
			Feed:        a.FeedTitle,
			Published:   a.Published,
			Excerpt:     htmlsafe.Excerpt(a.ContentHTML, excerptRunes),
			ImageURL:    a.ImageURL,
			Link:        a.Link,
			Read:        a.Read,
			Starred:     a.Starred,
			AccentClass: accentClass(a.FeedTitle),
		}
		if u, err := url.Parse(a.Link); err == nil {
			v.Domain = strings.TrimPrefix(u.Host, "www.")
		}
		v.ImageURL = s.imageURL(a.ImageURL)
		if v.ImageURL == "" && a.Link != "" {
			v.OGImage = s.ogURL(a.Link)
		}
		if len(out) < 3 {
			v.Priority = true
		}
		out = append(out, v)
	}
	return out
}

// accentClass derives a stable colour from the feed name.
func accentClass(seed string) string {
	sum := sha1.Sum([]byte(seed))
	n := int(sum[0]) % 8
	return "tile-a" + string(rune('0'+n))
}

// fibChunk groups articles in fours for the Fibonacci mosaic.
func fibChunk(items []articleView) [][]articleView {
	if len(items) == 0 {
		return nil
	}
	var pages [][]articleView
	for i := 0; i < len(items); i += 4 {
		end := min(i+4, len(items))
		pages = append(pages, items[i:end])
	}
	return pages
}

func chunk(items []articleView, size int) [][]articleView {
	if len(items) == 0 {
		return nil
	}
	var pages [][]articleView
	for i := 0; i < len(items); i += size {
		end := min(i+size, len(items))
		pages = append(pages, items[i:end])
	}
	return pages
}

// categories returns folder labels, cached for a few minutes.
func (s *Server) categories(ctx context.Context) []categoryView {
	s.catsMu.Lock()
	if time.Since(s.catsAt) < catsTTL && s.catsSet {
		out := s.cats
		s.catsMu.Unlock()
		return out
	}
	s.catsMu.Unlock()

	if s.src == nil {
		return nil
	}
	tags, err := s.src.Tags(ctx)
	if err != nil {
		s.log.Warn("cannot load categories", "error", err)
		return nil
	}
	var out []categoryView
	for _, t := range tags {
		if t.Type != "folder" {
			continue
		}
		label, ok := freshrss.LabelFromTagID(t.ID)
		if !ok || label == "" || label == "Uncategorized" {
			continue
		}
		out = append(out, categoryView{Label: label, Slug: categorySlug(label)})
	}
	s.catsMu.Lock()
	s.cats, s.catsAt, s.catsSet = out, time.Now(), true
	s.catsMu.Unlock()
	return out
}

// categoryNeighbours returns the previous/next category links for edge swipes.
func categoryNeighbours(cats []categoryView, label string) (prev, next string) {
	if len(cats) == 0 {
		return "", ""
	}
	if label == "" {
		return "", "/c/" + cats[0].Slug
	}
	for i, c := range cats {
		if c.Label != label {
			continue
		}
		prev = "/"
		if i > 0 {
			prev = "/c/" + cats[i-1].Slug
		}
		if i+1 < len(cats) {
			next = "/c/" + cats[i+1].Slug
		}
		return prev, next
	}
	return "", ""
}

func categorySlug(label string) string {
	sum := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(label))))
	return hex.EncodeToString(sum[:6])
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.FreshRSS.Configured() {
		s.render(w, r, http.StatusOK, "setup", s.baseData("Configuración pendiente", "home"))
		return
	}
	s.renderFeed(w, r, freshrss.ReadingList(), "")
}

// buildRegionalCards turns public feed items into the same cards used for
// FreshRSS articles.
func (s *Server) buildRegionalCards(items []regional.Item) []articleView {
	out := make([]articleView, 0, len(items))
	for _, it := range items {
		out = append(out, articleView{
			Key:         "",
			Lang:        it.Lang,
			Title:       it.Title,
			Feed:        it.Source,
			Published:   it.Published,
			Excerpt:     it.Summary,
			ImageURL:    s.imageURL(it.ImageURL),
			Link:        it.Link,
			Href:        it.Link,
			External:    true,
			AccentClass: accentClass(it.Source),
		})
	}
	return out
}

func (s *Server) handleRegional(w http.ResponseWriter, r *http.Request) {
	if s.regional == nil || !s.regional.Configured() {
		s.notFound(w, r)
		return
	}
	items, err := s.regional.Items(r.Context())
	data := s.baseData("Regional", "home")
	data.Bare = true
	data.Stream = true
	data.Category = "Regional"
	data.CategorySlug = "regional"
	data.Mosaic = isMosaic(r)
	data.ToggleHref = viewHref("Regional", "regional", !data.Mosaic)
	data.Categories = s.categories(r.Context())
	data.TranslateToggle = s.ai != nil
	if err != nil {
		data.DiagErr = "No se pudieron cargar los medios regionales."
		s.log.Warn("regional page failed", "error", err)
	}
	data.Articles = s.buildRegionalCards(items)
	s.render(w, r, http.StatusOK, "home", data)
}

func (s *Server) handleMenu(w http.ResponseWriter, r *http.Request) {
	data := s.baseData("Opciones", "home")
	data.Categories = s.categories(r.Context())
	data.Mosaic = isMosaic(r)
	data.ToggleHref = "/"
	if data.Mosaic {
		data.ToggleHref = "/?v=full"
	}
	if c := r.URL.Query().Get("cat"); c != "" {
		for _, cat := range data.Categories {
			if cat.Slug == c {
				data.Category = cat.Label
				break
			}
		}
	}
	s.render(w, r, http.StatusOK, "menu", data)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	data := s.baseData("Ajustes", "home")
	data.Mosaic = isMosaic(r)
	data.ToggleHref = "/"
	if data.Mosaic {
		data.ToggleHref = "/?v=full"
	}
	data.AIEnabled = s.ai != nil
	if s.ai != nil {
		data.AIModel = s.ai.Model()
	}
	if s.regional != nil {
		data.Regional = s.regional.Sources()
	}
	s.render(w, r, http.StatusOK, "ajustes", data)
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	data := s.baseData("Categorías", "feeds")
	data.Categories = s.categories(r.Context())
	s.render(w, r, http.StatusOK, "categorias", data)
}

func (s *Server) handleSaved(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.FreshRSS.Configured() {
		s.render(w, r, http.StatusOK, "setup", s.baseData("Configuración pendiente", "home"))
		return
	}
	s.renderFeed(w, r, freshrss.Starred(), "Guardados")
}

func (s *Server) handleCategory(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.FreshRSS.Configured() {
		s.render(w, r, http.StatusOK, "setup", s.baseData("Configuración pendiente", "home"))
		return
	}
	slug := strings.TrimSpace(r.PathValue("slug"))
	var label string
	for _, c := range s.categories(r.Context()) {
		if c.Slug == slug {
			label = c.Label
			break
		}
	}
	if label == "" {
		s.notFound(w, r)
		return
	}
	stream, err := freshrss.LabelStream(label)
	if err != nil {
		s.notFound(w, r)
		return
	}
	s.renderFeed(w, r, stream, label)
}

func isMosaic(r *http.Request) bool {
	// El mosaico es la vista principal; ?v=full vuelve a una por pantalla.
	return r.URL.Query().Get("v") != "full"
}

func viewHref(label, slug string, mosaic bool) string {
	base := "/"
	if label != "" {
		base = "/c/" + slug
	}
	if mosaic {
		return base
	}
	return base + "?v=full"
}

func (s *Server) renderFeed(w http.ResponseWriter, r *http.Request, stream freshrss.Stream, label string) {
	data := s.baseData(label, "home")
	data.Bare = true
	data.Stream = true
	data.Mosaic = isMosaic(r)
	data.Saved = label == "Guardados"
	data.AIEnabled = s.ai != nil
	if label == "" {
		data.Title = "Noticias"
	}
	data.Category = label
	data.CategorySlug = categorySlug(label)
	data.Categories = s.categories(r.Context())
	data.TranslateToggle = label == "Regional" && s.ai != nil
	data.PrevHref, data.NextHref = categoryNeighbours(data.Categories, label)

	if d, err := s.cachedDiag(r.Context(), false); err == nil {
		data.Diag, data.HasDiag = d, true
	}

	page, err := s.src.StreamContents(r.Context(), freshrss.StreamQuery{
		Stream:       stream,
		Count:        homePageSize,
		Continuation: r.URL.Query().Get("c"),
	})
	if err != nil {
		data.DiagErr = friendlyError(err)
		data.DiagKind = freshrss.Kind(err)
		s.log.Warn("cannot load articles", "stream", stream.ID(), "kind", data.DiagKind, "error", err)
		s.render(w, r, http.StatusBadGateway, "home", data)
		return
	}
	data.Articles = s.buildArticles(page.Items)
	data.Pages = fibChunk(data.Articles)
	data.Next = page.Continuation
	if s.media != nil {
		urls := make([]string, 0, 12)
		for _, it := range page.Items {
			if a := it.Article(); a.ImageURL != "" {
				urls = append(urls, a.ImageURL)
			}
			if len(urls) == 12 {
				break
			}
		}
		if len(urls) > 0 {
			go s.media.Warm(urls)
		}
	}
	if s.ai != nil {
		keys := make([]string, 0, 3)
		for _, a := range data.Articles {
			if a.Key != "" {
				keys = append(keys, a.Key)
			}
			if len(keys) == 3 {
				break
			}
		}
		go s.warmSummaries(keys)
	}
	if data.Saved {
		data.ToggleHref = "/guardados"
		if !data.Mosaic {
			data.ToggleHref = "/guardados?v=full"
		}
	} else {
		data.ToggleHref = viewHref(label, categorySlug(label), data.Mosaic)
	}
	s.render(w, r, http.StatusOK, "home", data)
}

// handleFeed returns the next batch of story cards as an HTML fragment.
func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	mosaic := r.URL.Query().Get("v") != "full"
	if !s.cfg.FreshRSS.Configured() {
		http.Error(w, "freshrss not configured", http.StatusServiceUnavailable)
		return
	}
	stream := freshrss.ReadingList()
	if r.URL.Query().Get("saved") == "1" {
		stream = freshrss.Starred()
	} else if slug := strings.TrimSpace(r.URL.Query().Get("cat")); slug != "" {
		var label string
		for _, c := range s.categories(r.Context()) {
			if c.Slug == slug {
				label = c.Label
				break
			}
		}
		if label == "" {
			http.Error(w, "unknown category", http.StatusNotFound)
			return
		}
		st, err := freshrss.LabelStream(label)
		if err != nil {
			http.Error(w, "unknown category", http.StatusNotFound)
			return
		}
		stream = st
	}

	page, err := s.src.StreamContents(r.Context(), freshrss.StreamQuery{
		Stream:       stream,
		Count:        feedBatchSize,
		Continuation: r.URL.Query().Get("c"),
	})
	if err != nil {
		s.log.Warn("feed fragment failed", "kind", freshrss.Kind(err), "error", err)
		http.Error(w, "cannot load more", http.StatusBadGateway)
		return
	}

	t, ok := s.pages["home"]
	if !ok {
		http.Error(w, "template missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Next-Continuation", page.Continuation)
	arts := s.buildArticles(page.Items)
	if mosaic {
		if err := t.ExecuteTemplate(w, "fib", pageData{Pages: fibChunk(arts)}); err != nil {
			s.log.Error("feed fragment render failed", "error", err)
		}
		return
	}
	if err := t.ExecuteTemplate(w, "cards", pageData{Articles: arts}); err != nil {
		s.log.Error("feed fragment render failed", "error", err)
	}
}

func (s *Server) handleArticle(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.FreshRSS.Configured() {
		s.render(w, r, http.StatusServiceUnavailable, "setup", s.baseData("Configuración pendiente", "home"))
		return
	}
	raw := strings.TrimSpace(r.PathValue("key"))
	if raw == "" || len(raw) > maxArticleKeySize {
		s.notFound(w, r)
		return
	}
	key, err := freshrss.ParseItemKey(raw)
	if err != nil {
		s.notFound(w, r)
		return
	}
	articles, err := s.src.Items(r.Context(), []string{key})
	if err != nil {
		data := s.baseData("Artículo", "home")
		data.DiagErr = friendlyError(err)
		data.DiagKind = freshrss.Kind(err)
		s.render(w, r, http.StatusBadGateway, "error", data)
		return
	}
	if len(articles) == 0 {
		s.notFound(w, r)
		return
	}
	a := articles[0]
	v := articleView{
		Key:       a.Key,
		Title:     a.Title,
		Feed:      a.FeedTitle,
		Published: a.Published,
		Link:      a.Link,
		Read:      a.Read,
		Starred:   a.Starred,
		ImageURL:  s.imageURL(a.ImageURL),
	}
	if v.ImageURL == "" && a.Link != "" {
		v.OGImage = s.ogURL(a.Link)
	}
	if u, perr := url.Parse(a.Link); perr == nil {
		v.Domain = strings.TrimPrefix(u.Host, "www.")
	}

	data := s.baseData(a.Title, "home")
	data.Bare = true
	data.AIEnabled = s.ai != nil
	data.FontSize = "1"
	if c, err := r.Cookie("rssr_font"); err == nil && (c.Value == "1" || c.Value == "2" || c.Value == "3") {
		data.FontSize = c.Value
	}
	switch r.URL.Query().Get("ok") {
	case "star":
		data.Notice = "Guardado en favoritos."
	case "read":
		data.Notice = "Estado de lectura actualizado."
	}
	if q := r.URL.Query().Get("error"); q != "" {
		data.Notice = "FreshRSS rechazó el cambio. Probá de nuevo."
	}
	data.Article = &v
	data.Content = template.HTML(htmlsafe.Fragment(a.ContentHTML))
	if strings.TrimSpace(a.ContentHTML) == "" {
		data.DiagErr = "FreshRSS no guardó contenido para este artículo. Abrí el original."
	}
	s.render(w, r, http.StatusOK, "article", data)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	data := s.baseData("No encontrado", "home")
	data.DiagErr = "Esa página o ese artículo no existe."
	s.render(w, r, http.StatusNotFound, "error", data)
}
