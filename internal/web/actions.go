package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/ldgnu/rssurrection/internal/freshrss"
)

// readerAction implements star/read as plain HTML forms so they work even
// when JavaScript does not run on the device.
func (s *Server) readerAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.FreshRSS.Configured() {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/a/"+r.PathValue("key"), http.StatusSeeOther)
			return
		}
		key, err := freshrss.ParseItemKey(strings.TrimSpace(r.PathValue("key")))
		back := "/a/" + key
		if err != nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		on := r.PostFormValue("on") == "1"

		var ferr error
		switch action {
		case "star":
			ferr = s.src.SetStarred(r.Context(), []string{key}, on)
		case "read":
			ferr = s.src.MarkRead(r.Context(), []string{key}, on)
		}
		if ferr != nil {
			s.log.Warn("reader action failed", "action", action, "kind", freshrss.Kind(ferr), "error", ferr)
			http.Redirect(w, r, back+"?error="+action, http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, back+"?ok="+action, http.StatusSeeOther)
	}
}

// handleFont stores the reader font size in a cookie and goes back.
func (s *Server) handleFont(w http.ResponseWriter, r *http.Request) {
	size := r.URL.Query().Get("size")
	switch size {
	case "1", "2", "3":
	default:
		size = "1"
	}
	http.SetCookie(w, &http.Cookie{
		Name: "rssr_font", Value: size, Path: "/", HttpOnly: false,
		Secure: s.secureRequest(r), SameSite: http.SameSiteLaxMode,
		MaxAge: 365 * 24 * 3600,
	})
	back := r.URL.Query().Get("back")
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") {
		back = "/"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// handleArticleActions renders the reader actions as plain HTML.
func (s *Server) handleArticleActions(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.FreshRSS.Configured() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	key, err := freshrss.ParseItemKey(strings.TrimSpace(r.PathValue("key")))
	if err != nil {
		s.notFound(w, r)
		return
	}
	items, err := s.src.Items(r.Context(), []string{key})
	if err != nil || len(items) == 0 {
		s.notFound(w, r)
		return
	}
	a := items[0]
	v := articleView{
		Key:       a.Key,
		Title:     a.Title,
		Feed:      a.FeedTitle,
		Published: a.Published,
		Link:      a.Link,
		Read:      a.Read,
		Starred:   a.Starred,
	}
	if u, perr := url.Parse(a.Link); perr == nil {
		v.Domain = strings.TrimPrefix(u.Host, "www.")
	}
	data := s.baseData("Opciones", "")
	data.Bare = true
	data.Article = &v
	data.FontSize = "1"
	if c, cerr := r.Cookie("rssr_font"); cerr == nil && (c.Value == "1" || c.Value == "2" || c.Value == "3") {
		data.FontSize = c.Value
	}
	switch r.URL.Query().Get("ok") {
	case "star":
		data.Notice = "Favorito actualizado."
	case "read":
		data.Notice = "Estado de lectura actualizado."
	}
	if r.URL.Query().Get("error") != "" {
		data.Notice = "FreshRSS rechazó el cambio."
	}
	s.render(w, r, http.StatusOK, "acciones", data)
}
