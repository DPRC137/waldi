package web

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"waldi/internal/i18n"
	"waldi/internal/store"
)

const minPasswordLen = 8

// handleChangePassword lets a signed-in user change their password from
// Settings, without going through the email-based reset flow.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.store == nil {
		s.renderSettingsPasswordError(w, r, user, "blog.settings.error.db")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderSettingsPasswordError(w, r, user, "blog.settings.error.form")
		return
	}

	current := r.FormValue("current_password")
	next := r.FormValue("new_password")

	if !checkPassword(user.PasswordHash, current) {
		s.renderSettingsPasswordError(w, r, user, "blog.settings.password.error.current")
		return
	}
	if len(next) < minPasswordLen {
		s.renderSettingsPasswordError(w, r, user, "blog.settings.password.error.length")
		return
	}

	hash, err := hashPassword(next)
	if err != nil {
		s.logger.Error("hashing password", "err", err)
		s.renderSettingsPasswordError(w, r, user, "blog.settings.error.save")
		return
	}
	if err := s.store.UpdatePasswordHash(r.Context(), user.ID, hash); err != nil {
		s.logger.Error("updating password", "err", err)
		s.renderSettingsPasswordError(w, r, user, "blog.settings.error.save")
		return
	}

	http.Redirect(w, r, "/settings?password=saved", http.StatusSeeOther)
}

func (s *Server) renderSettingsPasswordError(w http.ResponseWriter, r *http.Request, user *store.User, messageKey string) {
	pd := s.newPageData(r, user)
	pd.Title = pd.T("blog.settings.title")
	pd.SEO = noindexSEO()
	view := blogSettingsViewFor(*user, s.baseDomain)
	view.Pages = s.pagesViewFor(r.Context(), user.ID)
	view.MaxPages = store.MaxPagesPerUser
	view.PasswordError = pd.T(messageKey)
	pd.BlogSettings = view
	s.renderer.RenderStatus(w, http.StatusBadRequest, "blog_settings.html", pd)
}

// handleDeleteAccount permanently deletes the signed-in user's account
// after confirming their password. Posts, sessions, follows, and letters
// are removed along with the users row by the cascading foreign keys.
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.store == nil {
		s.renderSettingsDeleteError(w, r, user, "blog.settings.error.db")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderSettingsDeleteError(w, r, user, "blog.settings.error.form")
		return
	}

	if !checkPassword(user.PasswordHash, r.FormValue("current_password")) {
		s.renderSettingsDeleteError(w, r, user, "blog.settings.delete.error.password")
		return
	}

	if err := s.store.DeleteUser(r.Context(), user.ID); err != nil {
		s.logger.Error("deleting account", "user_id", user.ID, "err", err)
		s.renderSettingsDeleteError(w, r, user, "blog.settings.error.save")
		return
	}

	domain := sessionCookieDomain(r.Host, s.baseDomain)
	secure := requestScheme(r) == "https"
	clearSessionCookie(w, domain, secure)
	clearBridgeCookie(w, domain, secure)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) renderSettingsDeleteError(w http.ResponseWriter, r *http.Request, user *store.User, messageKey string) {
	pd := s.newPageData(r, user)
	pd.Title = pd.T("blog.settings.title")
	pd.SEO = noindexSEO()
	view := blogSettingsViewFor(*user, s.baseDomain)
	view.Pages = s.pagesViewFor(r.Context(), user.ID)
	view.MaxPages = store.MaxPagesPerUser
	view.DeleteError = pd.T(messageKey)
	pd.BlogSettings = view
	s.renderer.RenderStatus(w, http.StatusBadRequest, "blog_settings.html", pd)
}

type exportPost struct {
	Title       string  `json:"title"`
	Slug        string  `json:"slug"`
	Status      string  `json:"status"`
	HTML        string  `json:"html"`
	Doc         any     `json:"doc"`
	PublishedAt *string `json:"published_at,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

type exportPostPage struct {
	Lang  string
	Dir   string
	Title string
	HTML  template.HTML
}

var exportPostTemplate = template.Must(template.New("post").Parse(`<!DOCTYPE html>
<html lang="{{.Lang}}" dir="{{.Dir}}">
<head>
<meta charset="utf-8">
<title>{{.Title}}</title>
</head>
<body>
<h1>{{.Title}}</h1>
{{.HTML}}
</body>
</html>
`))

// handleExportPosts streams every post a user has written as a zip archive
// containing a posts.json dump and individual HTML files, so writers can
// always take their words with them.
func (s *Server) handleExportPosts(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.store == nil {
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}

	posts, err := s.store.AllPostsByUser(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("exporting posts", "err", err)
		http.Error(w, "export failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+user.Username+`-export.zip"`)
	if err := writeExportZip(w, posts); err != nil {
		s.logger.Error("writing export", "err", err)
	}
}

func writeExportZip(w io.Writer, posts []store.Post) error {
	out := make([]exportPost, 0, len(posts))
	for _, p := range posts {
		var doc any
		if err := json.Unmarshal(p.Doc, &doc); err != nil {
			doc = nil
		}
		var publishedAt *string
		if p.PublishedAt != nil {
			formatted := p.PublishedAt.UTC().Format("2006-01-02T15:04:05Z")
			publishedAt = &formatted
		}
		out = append(out, exportPost{
			Title:       p.Title,
			Slug:        p.Slug,
			Status:      p.Status,
			HTML:        p.HTML,
			Doc:         doc,
			PublishedAt: publishedAt,
			CreatedAt:   p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}

	zw := zip.NewWriter(w)
	jsonF, err := zw.Create("posts.json")
	if err != nil {
		return fmt.Errorf("creating posts.json: %w", err)
	}
	enc := json.NewEncoder(jsonF)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("writing posts.json: %w", err)
	}

	for _, p := range posts {
		name := "draft-" + p.Slug + ".html"
		if p.PublishedAt != nil {
			name = p.PublishedAt.UTC().Format("2006-01-02") + "-" + p.Slug + ".html"
		}
		htmlF, err := zw.Create("posts/" + name)
		if err != nil {
			return fmt.Errorf("creating %s: %w", name, err)
		}
		err = exportPostTemplate.Execute(htmlF, exportPostPage{
			Lang:  p.BlogLang,
			Dir:   i18n.Dir(p.BlogLang),
			Title: p.Title,
			HTML:  template.HTML(p.HTML),
		})
		if err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("closing zip: %w", err)
	}
	return nil
}
