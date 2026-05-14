package server

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"speedrss/internal/auth"
	"speedrss/internal/backup"
	"speedrss/internal/feed"
	"speedrss/internal/store"
	"speedrss/internal/web"
)

const (
	sessionCookie = "speedrss_session"
	sessionDays   = 30
)

type Server struct {
	store     *store.Store
	feeds     *feed.Client
	templates *template.Template
}

type PageData struct {
	Title           string
	User            *store.User
	Feeds           []store.Feed
	Articles        []store.Article
	SelectedFeedID  int64
	SelectedArticle *store.Article
	Query           string
	View            string
	Message         string
	Error           string
	UnreadTotal     int
	FavoriteTotal   int
	ArticleTotal    int
}

func New(dbPath string) (*Server, error) {
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	return &Server{store: st, feeds: feed.NewClient(), templates: web.Templates()}, nil
}

func (s *Server) Close() error { return s.store.Close() }

func (s *Server) ListenAndServe(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /static/app.css", s.handleCSS)
	mux.HandleFunc("GET /login", s.handleLogin)
	mux.HandleFunc("POST /login", s.handleLoginPost)
	mux.HandleFunc("GET /setup", s.handleSetup)
	mux.HandleFunc("POST /setup", s.handleSetupPost)
	mux.HandleFunc("POST /logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("GET /", s.requireAuth(s.handleHome))
	mux.HandleFunc("POST /feeds", s.requireAuth(s.handleAddFeed))
	mux.HandleFunc("POST /feeds/{id}/refresh", s.requireAuth(s.handleRefreshFeed))
	mux.HandleFunc("POST /refresh", s.requireAuth(s.handleRefreshAll))
	mux.HandleFunc("GET /articles/{id}", s.requireAuth(s.handleArticle))
	mux.HandleFunc("POST /articles/{id}/read", s.requireAuth(s.handleMarkRead))
	mux.HandleFunc("POST /articles/{id}/favorite", s.requireAuth(s.handleFavorite))
	mux.HandleFunc("GET /backup", s.requireAuth(s.handleBackup))
	return http.ListenAndServe(addr, securityHeaders(mux))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

type userKey struct{}

func userFromContext(ctx context.Context) *store.User {
	user, _ := ctx.Value(userKey{}).(*store.User)
	return user
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := s.currentUser(r)
		if err != nil {
			if !s.store.HasUsers() {
				http.Redirect(w, r, "/setup", http.StatusSeeOther)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey{}, user)))
	}
}

func (s *Server) currentUser(r *http.Request) (*store.User, error) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return nil, errors.New("no session")
	}
	user, expires, err := s.store.UserBySession(cookie.Value)
	if err != nil {
		return nil, err
	}
	if time.Now().After(expires) {
		s.store.DeleteSession(cookie.Value)
		return nil, errors.New("expired session")
	}
	return user, nil
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.store.HasUsers() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, "setup", PageData{Title: "Set up SpeedRSS"})
}

func (s *Server) handleSetupPost(w http.ResponseWriter, r *http.Request) {
	if s.store.HasUsers() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if username == "" || len(password) < 8 {
		s.render(w, "setup", PageData{Title: "Set up SpeedRSS", Error: "Use a username and a password with at least 8 characters."})
		return
	}
	salt, hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "could not create user", http.StatusInternalServerError)
		return
	}
	userID, err := s.store.CreateUser(username, salt, hash)
	if err != nil {
		s.render(w, "setup", PageData{Title: "Set up SpeedRSS", Error: "Could not create user."})
		return
	}
	s.createSession(w, userID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.store.HasUsers() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.render(w, "login", PageData{Title: "Log in"})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	id, salt, hash, err := s.store.UserPassword(username)
	if err != nil || !auth.CheckPassword(password, salt, hash) {
		s.render(w, "login", PageData{Title: "Log in", Error: "Invalid username or password."})
		return
	}
	s.createSession(w, id)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) createSession(w http.ResponseWriter, userID int64) {
	token := auth.RandomToken(32)
	expires := time.Now().Add(sessionDays * 24 * time.Hour)
	_ = s.store.CreateSession(token, userID, expires)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: expires, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.store.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	s.render(w, "app", s.pageData(r))
}

func (s *Server) handleArticle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("mark") == "read" {
		s.store.SetRead(id, true)
	}
	data := s.pageData(r)
	article, err := s.store.Article(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data.SelectedArticle = article
	data.SelectedFeedID = article.FeedID
	s.render(w, "app", data)
}

func (s *Server) handleAddFeed(w http.ResponseWriter, r *http.Request) {
	feedURL := strings.TrimSpace(r.FormValue("feed_url"))
	if feedURL == "" {
		redirectError(w, r, "Feed URL is required")
		return
	}
	if err := s.upsertFeed(feedURL, 0); err != nil {
		redirectError(w, r, "Could not read feed: "+err.Error())
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleRefreshFeed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	feedURL, err := s.store.FeedURL(id)
	if err != nil {
		redirectError(w, r, err.Error())
		return
	}
	if err := s.upsertFeed(feedURL, id); err != nil {
		s.store.MarkFeedError(id, err)
		redirectError(w, r, "Could not refresh feed: "+err.Error())
		return
	}
	http.Redirect(w, r, "/?feed="+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) handleRefreshAll(w http.ResponseWriter, r *http.Request) {
	var failures []string
	for _, id := range s.store.ListFeedIDs() {
		feedURL, err := s.store.FeedURL(id)
		if err == nil {
			err = s.upsertFeed(feedURL, id)
		}
		if err != nil {
			s.store.MarkFeedError(id, err)
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		redirectError(w, r, strings.Join(failures, "; "))
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) upsertFeed(feedURL string, existingID int64) error {
	f, err := s.feeds.Fetch(feedURL)
	if err != nil {
		return err
	}
	title := store.FirstNonEmpty(f.Title, store.HostLabel(feedURL))
	id, err := s.store.UpsertFeed(title, feedURL, f.SiteURL, f.Description, f.FaviconURL)
	if err != nil {
		return err
	}
	if existingID > 0 {
		id = existingID
	}
	return s.store.SaveArticles(id, f.Items)
}

func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.store.SetRead(id, r.FormValue("read") != "false")
	http.Redirect(w, r, "/articles/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) handleFavorite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.store.SetFavorite(id, r.FormValue("favorite") != "false")
	http.Redirect(w, r, "/articles/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="speedrss-data.zip"`)
	_ = backup.Write(w, "data")
}

func (s *Server) handleCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write([]byte(web.CSS))
}

func (s *Server) pageData(r *http.Request) PageData {
	feedID, _ := strconv.ParseInt(r.URL.Query().Get("feed"), 10, 64)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "all"
	}
	feeds := s.store.ListFeeds()
	articles := s.store.ListArticles(feedID, query, view)
	unread, favs, total := totals(feeds)
	return PageData{
		Title:          "SpeedRSS",
		User:           userFromContext(r.Context()),
		Feeds:          feeds,
		Articles:       articles,
		SelectedFeedID: feedID,
		Query:          query,
		View:           view,
		Error:          r.URL.Query().Get("error"),
		UnreadTotal:    unread,
		FavoriteTotal:  favs,
		ArticleTotal:   total,
	}
}

func totals(feeds []store.Feed) (int, int, int) {
	var unread, favs, total int
	for _, feed := range feeds {
		unread += feed.UnreadCount
		favs += feed.FavoriteCount
		total += feed.TotalCount
	}
	return unread, favs, total
}

func (s *Server) render(w http.ResponseWriter, name string, data PageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func redirectError(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?error="+url.QueryEscape(msg), http.StatusSeeOther)
}
