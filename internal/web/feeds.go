package web

import (
	"net/http"
	"sort"
	"strings"

	"github.com/ldgnu/rssurrection/internal/freshrss"
)

type feedRow struct {
	ID       string
	Title    string
	URL      string
	Category string
	Unread   int
}

type feedsData struct {
	pageData
	Feeds []feedRow
	Sort  string
}

func (s *Server) handleFeeds(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.FreshRSS.Configured() {
		s.render(w, r, http.StatusOK, "setup", s.baseData("Configuración pendiente", "home"))
		return
	}
	sortMode := r.URL.Query().Get("sort")
	if sortMode != "unread" && sortMode != "category" {
		sortMode = "name"
	}

	subs, err := s.src.Subscriptions(r.Context())
	data := feedsData{pageData: s.baseData("Feeds", "feeds"), Sort: sortMode}
	if err != nil {
		data.DiagErr = friendlyError(err)
		data.DiagKind = freshrss.Kind(err)
		s.renderFeeds(w, r, data)
		return
	}
	counts := map[string]int{}
	if uc, cerr := s.src.UnreadCounts(r.Context()); cerr == nil {
		for _, e := range uc.Entries {
			counts[e.ID] = e.Count
		}
	}

	for _, sub := range subs {
		row := feedRow{
			ID:     strings.TrimPrefix(sub.ID, "feed/"),
			Title:  sub.Title,
			URL:    sub.URL,
			Unread: counts[sub.ID],
		}
		if len(sub.Categories) > 0 {
			row.Category = sub.Categories[0].Label
		}
		data.Feeds = append(data.Feeds, row)
	}

	switch sortMode {
	case "unread":
		sort.SliceStable(data.Feeds, func(i, j int) bool {
			if data.Feeds[i].Unread != data.Feeds[j].Unread {
				return data.Feeds[i].Unread > data.Feeds[j].Unread
			}
			return strings.ToLower(data.Feeds[i].Title) < strings.ToLower(data.Feeds[j].Title)
		})
	case "category":
		sort.SliceStable(data.Feeds, func(i, j int) bool {
			if data.Feeds[i].Category != data.Feeds[j].Category {
				return strings.ToLower(data.Feeds[i].Category) < strings.ToLower(data.Feeds[j].Category)
			}
			return strings.ToLower(data.Feeds[i].Title) < strings.ToLower(data.Feeds[j].Title)
		})
	default:
		sort.SliceStable(data.Feeds, func(i, j int) bool {
			return strings.ToLower(data.Feeds[i].Title) < strings.ToLower(data.Feeds[j].Title)
		})
	}

	s.renderFeeds(w, r, data)
}

func (s *Server) handleRenameFeed(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.FreshRSS.Configured() {
		http.Error(w, "freshrss not configured", http.StatusServiceUnavailable)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.PostFormValue("id"))
	title := strings.TrimSpace(r.PostFormValue("title"))
	sortMode := r.PostFormValue("sort")
	if sortMode == "" {
		sortMode = "name"
	}
	if id == "" || title == "" {
		http.Redirect(w, r, "/feeds?sort="+sortMode+"&error=1", http.StatusSeeOther)
		return
	}
	if err := s.src.RenameFeed(r.Context(), id, title); err != nil {
		s.log.Warn("rename feed failed", "kind", freshrss.Kind(err), "error", err)
		http.Redirect(w, r, "/feeds?sort="+sortMode+"&error=1", http.StatusSeeOther)
		return
	}
	s.invalidateTags()
	http.Redirect(w, r, "/feeds?sort="+sortMode+"&ok=1", http.StatusSeeOther)
}

func (s *Server) renderFeeds(w http.ResponseWriter, r *http.Request, data feedsData) {
	t, ok := s.pages["feeds"]
	if !ok {
		http.Error(w, "template missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		s.log.Error("render feeds failed", "error", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
	}
}
