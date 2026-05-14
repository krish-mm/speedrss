package main

import (
	"archive/zip"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	addr           = ":8080"
	dataDir        = "data"
	dbPath         = "data/speedrss.db"
	sessionCookie  = "speedrss_session"
	maxFeedBody    = 20 << 20
	sessionDays    = 30
	passwordIters  = 160000
	passwordKeyLen = 32
)

type App struct {
	db        *sql.DB
	templates *template.Template
	client    *http.Client
}

type User struct {
	ID       int64
	Username string
}

type Feed struct {
	ID              int64
	Title           string
	SiteURL         string
	FeedURL         string
	Description     string
	FaviconURL      string
	LastFetchedAt   sql.NullTime
	LastError       sql.NullString
	UnreadCount     int
	FavoriteCount   int
	TotalCount      int
	DisplayHostname string
}

type Article struct {
	ID          int64
	FeedID      int64
	FeedTitle   string
	Title       string
	URL         string
	Author      string
	SummaryHTML template.HTML
	ContentHTML template.HTML
	PublishedAt sql.NullTime
	CreatedAt   time.Time
	IsRead      bool
	IsFavorite  bool
}

type PageData struct {
	Title           string
	User            *User
	HasUser         bool
	Feeds           []Feed
	Articles        []Article
	SelectedFeedID  int64
	SelectedArticle *Article
	Query           string
	View            string
	Message         string
	Error           string
	UnreadTotal     int
	FavoriteTotal   int
}

type parsedFeed struct {
	Title       string
	Description string
	SiteURL     string
	Items       []parsedItem
}

type parsedItem struct {
	GUID        string
	Title       string
	URL         string
	Author      string
	SummaryHTML string
	ContentHTML string
	PublishedAt sql.NullTime
}

func main() {
	restorePath := flag.String("restore", "", "restore a SpeedRSS data backup zip before starting")
	flag.Parse()

	if *restorePath != "" {
		if err := restoreBackup(*restorePath); err != nil {
			log.Fatal(err)
		}
		log.Printf("restored %s into %s", *restorePath, dataDir)
		return
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatal(err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	app := &App{
		db:        db,
		templates: parseTemplates(),
		client: &http.Client{
			Timeout: 25 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
	}
	if err := app.migrate(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /static/app.css", app.handleCSS)
	mux.HandleFunc("GET /login", app.handleLogin)
	mux.HandleFunc("POST /login", app.handleLoginPost)
	mux.HandleFunc("POST /logout", app.requireAuth(app.handleLogout))
	mux.HandleFunc("GET /setup", app.handleSetup)
	mux.HandleFunc("POST /setup", app.handleSetupPost)
	mux.HandleFunc("GET /", app.requireAuth(app.handleHome))
	mux.HandleFunc("POST /feeds", app.requireAuth(app.handleAddFeed))
	mux.HandleFunc("POST /feeds/{id}/refresh", app.requireAuth(app.handleRefreshFeed))
	mux.HandleFunc("POST /refresh", app.requireAuth(app.handleRefreshAll))
	mux.HandleFunc("GET /articles/{id}", app.requireAuth(app.handleArticle))
	mux.HandleFunc("POST /articles/{id}/read", app.requireAuth(app.handleMarkRead))
	mux.HandleFunc("POST /articles/{id}/favorite", app.requireAuth(app.handleFavorite))
	mux.HandleFunc("GET /backup", app.requireAuth(app.handleBackup))

	log.Printf("SpeedRSS running at http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, securityHeaders(mux)))
}

func (a *App) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_salt TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS feeds (
			id INTEGER PRIMARY KEY,
			title TEXT NOT NULL,
			feed_url TEXT NOT NULL UNIQUE,
			site_url TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			favicon_url TEXT NOT NULL DEFAULT '',
			last_fetched_at DATETIME,
			last_error TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS articles (
			id INTEGER PRIMARY KEY,
			feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
			guid TEXT NOT NULL,
			url TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL,
			author TEXT NOT NULL DEFAULT '',
			summary_html TEXT NOT NULL DEFAULT '',
			content_html TEXT NOT NULL DEFAULT '',
			published_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(feed_id, guid)
		)`,
		`CREATE TABLE IF NOT EXISTS article_state (
			article_id INTEGER PRIMARY KEY REFERENCES articles(id) ON DELETE CASCADE,
			is_read INTEGER NOT NULL DEFAULT 0,
			is_favorite INTEGER NOT NULL DEFAULT 0,
			read_at DATETIME,
			favorited_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS article_search (
			article_id INTEGER PRIMARY KEY REFERENCES articles(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			author TEXT NOT NULL,
			body TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_article_search_title ON article_search(title)`,
		`CREATE INDEX IF NOT EXISTS idx_articles_feed_published ON articles(feed_id, published_at DESC, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)`,
	}
	for _, stmt := range stmts {
		if _, err := a.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (a *App) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := a.currentUser(r)
		if err != nil {
			if !a.hasUsers() {
				http.Redirect(w, r, "/setup", http.StatusSeeOther)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), userKey{}, user)
		next(w, r.WithContext(ctx))
	}
}

type userKey struct{}

func userFromContext(ctx context.Context) *User {
	user, _ := ctx.Value(userKey{}).(*User)
	return user
}

func (a *App) hasUsers() bool {
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n > 0
}

func (a *App) currentUser(r *http.Request) (*User, error) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return nil, errors.New("no session")
	}
	var user User
	var expires time.Time
	err = a.db.QueryRow(`
		SELECT users.id, users.username, sessions.expires_at
		FROM sessions
		JOIN users ON users.id = sessions.user_id
		WHERE sessions.token = ?`, cookie.Value).Scan(&user.ID, &user.Username, &expires)
	if err != nil {
		return nil, err
	}
	if time.Now().After(expires) {
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE token = ?`, cookie.Value)
		return nil, errors.New("expired session")
	}
	return &user, nil
}

func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	if a.hasUsers() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	a.render(w, "setup", PageData{Title: "Set up SpeedRSS"})
}

func (a *App) handleSetupPost(w http.ResponseWriter, r *http.Request) {
	if a.hasUsers() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if username == "" || len(password) < 8 {
		a.render(w, "setup", PageData{Title: "Set up SpeedRSS", Error: "Use a username and a password with at least 8 characters."})
		return
	}
	salt, hash, err := hashPassword(password)
	if err != nil {
		http.Error(w, "could not create user", http.StatusInternalServerError)
		return
	}
	res, err := a.db.Exec(`INSERT INTO users (username, password_salt, password_hash) VALUES (?, ?, ?)`, username, salt, hash)
	if err != nil {
		a.render(w, "setup", PageData{Title: "Set up SpeedRSS", Error: "Could not create user."})
		return
	}
	userID, _ := res.LastInsertId()
	a.createSession(w, userID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.hasUsers() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	a.render(w, "login", PageData{Title: "Log in"})
}

func (a *App) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	var id int64
	var salt, hash string
	err := a.db.QueryRow(`SELECT id, password_salt, password_hash FROM users WHERE username = ?`, username).Scan(&id, &salt, &hash)
	if err != nil || !checkPassword(password, salt, hash) {
		a.render(w, "login", PageData{Title: "Log in", Error: "Invalid username or password."})
		return
	}
	a.createSession(w, id)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE token = ?`, cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) createSession(w http.ResponseWriter, userID int64) {
	token := randomToken(32)
	expires := time.Now().Add(sessionDays * 24 * time.Hour)
	_, _ = a.db.Exec(`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`, token, userID, expires)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	data := a.pageData(r)
	a.render(w, "app", data)
}

func (a *App) handleArticle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("mark") == "read" {
		_, _ = a.db.Exec(`INSERT INTO article_state (article_id, is_read, read_at) VALUES (?, 1, CURRENT_TIMESTAMP)
			ON CONFLICT(article_id) DO UPDATE SET is_read = 1, read_at = CURRENT_TIMESTAMP`, id)
	}
	data := a.pageData(r)
	article, err := a.getArticle(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data.SelectedArticle = article
	data.SelectedFeedID = article.FeedID
	a.render(w, "app", data)
}

func (a *App) handleAddFeed(w http.ResponseWriter, r *http.Request) {
	feedURL := strings.TrimSpace(r.FormValue("feed_url"))
	if feedURL == "" {
		http.Redirect(w, r, "/?error=Feed+URL+is+required", http.StatusSeeOther)
		return
	}
	feed, err := a.fetchFeed(feedURL)
	if err != nil {
		http.Redirect(w, r, "/?error="+url.QueryEscape("Could not read feed: "+err.Error()), http.StatusSeeOther)
		return
	}
	siteURL := normalizeURL(feed.SiteURL)
	if siteURL == "" {
		siteURL = siteFromFeedURL(feedURL)
	}
	title := strings.TrimSpace(feed.Title)
	if title == "" {
		title = hostLabel(feedURL)
	}
	favicon := faviconURL(siteURL)
	res, err := a.db.Exec(`INSERT INTO feeds (title, feed_url, site_url, description, favicon_url, last_fetched_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(feed_url) DO UPDATE SET title = excluded.title, site_url = excluded.site_url, description = excluded.description, favicon_url = excluded.favicon_url, last_error = NULL`,
		title, feedURL, siteURL, strings.TrimSpace(feed.Description), favicon)
	if err != nil {
		http.Redirect(w, r, "/?error="+url.QueryEscape("Could not save feed."), http.StatusSeeOther)
		return
	}
	feedID, _ := res.LastInsertId()
	if feedID == 0 {
		_ = a.db.QueryRow(`SELECT id FROM feeds WHERE feed_url = ?`, feedURL).Scan(&feedID)
	}
	if err := a.saveItems(feedID, feed.Items); err != nil {
		http.Redirect(w, r, "/?error="+url.QueryEscape("Feed saved, but articles could not be indexed."), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?feed="+strconv.FormatInt(feedID, 10), http.StatusSeeOther)
}

func (a *App) handleRefreshFeed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.refreshFeed(id); err != nil {
		http.Redirect(w, r, "/?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?feed="+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (a *App) handleRefreshAll(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(`SELECT id FROM feeds ORDER BY title`)
	if err != nil {
		http.Redirect(w, r, "/?error="+url.QueryEscape("Could not refresh feeds."), http.StatusSeeOther)
		return
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	var failures []string
	for _, id := range ids {
		if err := a.refreshFeed(id); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		http.Redirect(w, r, "/?error="+url.QueryEscape(strings.Join(failures, "; ")), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) refreshFeed(id int64) error {
	var feedURL string
	if err := a.db.QueryRow(`SELECT feed_url FROM feeds WHERE id = ?`, id).Scan(&feedURL); err != nil {
		return errors.New("feed not found")
	}
	feed, err := a.fetchFeed(feedURL)
	if err != nil {
		_, _ = a.db.Exec(`UPDATE feeds SET last_error = ? WHERE id = ?`, err.Error(), id)
		return fmt.Errorf("could not refresh %s: %w", feedURL, err)
	}
	siteURL := normalizeURL(feed.SiteURL)
	if siteURL == "" {
		siteURL = siteFromFeedURL(feedURL)
	}
	title := strings.TrimSpace(feed.Title)
	if title == "" {
		title = hostLabel(feedURL)
	}
	_, _ = a.db.Exec(`UPDATE feeds SET title = ?, site_url = ?, description = ?, favicon_url = ?, last_fetched_at = CURRENT_TIMESTAMP, last_error = NULL WHERE id = ?`,
		title, siteURL, strings.TrimSpace(feed.Description), faviconURL(siteURL), id)
	return a.saveItems(id, feed.Items)
}

func (a *App) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	read := r.FormValue("read") != "false"
	var readAt any = nil
	if read {
		readAt = time.Now()
	}
	_, _ = a.db.Exec(`INSERT INTO article_state (article_id, is_read, read_at) VALUES (?, ?, ?)
		ON CONFLICT(article_id) DO UPDATE SET is_read = excluded.is_read, read_at = excluded.read_at`, id, boolInt(read), readAt)
	http.Redirect(w, r, "/articles/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (a *App) handleFavorite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	favorite := r.FormValue("favorite") != "false"
	var favAt any = nil
	if favorite {
		favAt = time.Now()
	}
	_, _ = a.db.Exec(`INSERT INTO article_state (article_id, is_favorite, favorited_at) VALUES (?, ?, ?)
		ON CONFLICT(article_id) DO UPDATE SET is_favorite = excluded.is_favorite, favorited_at = excluded.favorited_at`, id, boolInt(favorite), favAt)
	http.Redirect(w, r, "/articles/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (a *App) handleBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="speedrss-data.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	_ = filepath.WalkDir(dataDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name, _ := filepath.Rel(".", p)
		f, err := zw.Create(name)
		if err != nil {
			return nil
		}
		src, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer src.Close()
		_, _ = io.Copy(f, src)
		return nil
	})
}

func restoreBackup(zipPath string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	tempDir, err := os.MkdirTemp(filepath.Dir(dataDir), "speedrss-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	for _, file := range zr.File {
		clean := filepath.Clean(file.Name)
		if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("unsafe backup path: %s", file.Name)
		}
		if clean == dataDir {
			continue
		}
		if !strings.HasPrefix(clean, dataDir+string(os.PathSeparator)) {
			return fmt.Errorf("backup must contain files under %s/", dataDir)
		}
		target := filepath.Join(tempDir, strings.TrimPrefix(clean, dataDir+string(os.PathSeparator)))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			_ = src.Close()
			return err
		}
		_, copyErr := io.Copy(dst, src)
		closeErr := dst.Close()
		_ = src.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}

	if _, err := os.Stat(filepath.Join(tempDir, "speedrss.db")); err != nil {
		return errors.New("backup does not contain data/speedrss.db")
	}

	oldDir := dataDir + ".old-" + time.Now().Format("20060102150405")
	if _, err := os.Stat(dataDir); err == nil {
		if err := os.Rename(dataDir, oldDir); err != nil {
			return err
		}
	}
	if err := os.Rename(tempDir, dataDir); err != nil {
		if _, statErr := os.Stat(oldDir); statErr == nil {
			_ = os.Rename(oldDir, dataDir)
		}
		return err
	}
	return nil
}

func (a *App) pageData(r *http.Request) PageData {
	feedID, _ := strconv.ParseInt(r.URL.Query().Get("feed"), 10, 64)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "all"
	}
	feeds := a.listFeeds()
	articles := a.listArticles(feedID, query, view)
	unread, favs := totals(feeds)
	return PageData{
		Title:          "SpeedRSS",
		User:           userFromContext(r.Context()),
		HasUser:        a.hasUsers(),
		Feeds:          feeds,
		Articles:       articles,
		SelectedFeedID: feedID,
		Query:          query,
		View:           view,
		Message:        r.URL.Query().Get("message"),
		Error:          r.URL.Query().Get("error"),
		UnreadTotal:    unread,
		FavoriteTotal:  favs,
	}
}

func (a *App) listFeeds() []Feed {
	rows, err := a.db.Query(`
		SELECT feeds.id, feeds.title, feeds.site_url, feeds.feed_url, feeds.description, feeds.favicon_url, feeds.last_fetched_at, feeds.last_error,
			COALESCE(SUM(CASE WHEN COALESCE(article_state.is_read, 0) = 0 THEN 1 ELSE 0 END), 0) AS unread_count,
			COALESCE(SUM(CASE WHEN COALESCE(article_state.is_favorite, 0) = 1 THEN 1 ELSE 0 END), 0) AS favorite_count,
			COUNT(articles.id) AS total_count
		FROM feeds
		LEFT JOIN articles ON articles.feed_id = feeds.id
		LEFT JOIN article_state ON article_state.article_id = articles.id
		GROUP BY feeds.id
		ORDER BY LOWER(feeds.title)`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var feeds []Feed
	for rows.Next() {
		var f Feed
		if err := rows.Scan(&f.ID, &f.Title, &f.SiteURL, &f.FeedURL, &f.Description, &f.FaviconURL, &f.LastFetchedAt, &f.LastError, &f.UnreadCount, &f.FavoriteCount, &f.TotalCount); err == nil {
			f.DisplayHostname = hostLabel(firstNonEmpty(f.SiteURL, f.FeedURL))
			feeds = append(feeds, f)
		}
	}
	return feeds
}

func (a *App) listArticles(feedID int64, query, view string) []Article {
	var rows *sql.Rows
	var err error
	if query != "" {
		where, searchArgs := searchWhere(query)
		sqlText := `
			SELECT articles.id, articles.feed_id, feeds.title, articles.title, articles.url, articles.author, articles.summary_html, articles.content_html,
				articles.published_at, articles.created_at, COALESCE(article_state.is_read, 0), COALESCE(article_state.is_favorite, 0)
			FROM article_search
			JOIN articles ON articles.id = article_search.article_id
			JOIN feeds ON feeds.id = articles.feed_id
			LEFT JOIN article_state ON article_state.article_id = articles.id
			WHERE ` + where
		args := searchArgs
		if feedID > 0 {
			sqlText += ` AND articles.feed_id = ?`
			args = append(args, feedID)
		}
		if view == "unread" {
			sqlText += ` AND COALESCE(article_state.is_read, 0) = 0`
		}
		if view == "favorites" {
			sqlText += ` AND COALESCE(article_state.is_favorite, 0) = 1`
		}
		sqlText += ` ORDER BY COALESCE(articles.published_at, articles.created_at) DESC LIMIT 200`
		rows, err = a.db.Query(sqlText, args...)
	} else {
		sqlText := `
			SELECT articles.id, articles.feed_id, feeds.title, articles.title, articles.url, articles.author, articles.summary_html, articles.content_html,
				articles.published_at, articles.created_at, COALESCE(article_state.is_read, 0), COALESCE(article_state.is_favorite, 0)
			FROM articles
			JOIN feeds ON feeds.id = articles.feed_id
			LEFT JOIN article_state ON article_state.article_id = articles.id
			WHERE 1 = 1`
		args := []any{}
		if feedID > 0 {
			sqlText += ` AND articles.feed_id = ?`
			args = append(args, feedID)
		}
		if view == "unread" {
			sqlText += ` AND COALESCE(article_state.is_read, 0) = 0`
		}
		if view == "favorites" {
			sqlText += ` AND COALESCE(article_state.is_favorite, 0) = 1`
		}
		sqlText += ` ORDER BY COALESCE(articles.published_at, articles.created_at) DESC LIMIT 200`
		rows, err = a.db.Query(sqlText, args...)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanArticles(rows)
}

func (a *App) getArticle(id int64) (*Article, error) {
	row := a.db.QueryRow(`
		SELECT articles.id, articles.feed_id, feeds.title, articles.title, articles.url, articles.author, articles.summary_html, articles.content_html,
			articles.published_at, articles.created_at, COALESCE(article_state.is_read, 0), COALESCE(article_state.is_favorite, 0)
		FROM articles
		JOIN feeds ON feeds.id = articles.feed_id
		LEFT JOIN article_state ON article_state.article_id = articles.id
		WHERE articles.id = ?`, id)
	var article Article
	var summary, content string
	var read, favorite int
	err := row.Scan(&article.ID, &article.FeedID, &article.FeedTitle, &article.Title, &article.URL, &article.Author, &summary, &content, &article.PublishedAt, &article.CreatedAt, &read, &favorite)
	if err != nil {
		return nil, err
	}
	article.SummaryHTML = template.HTML(sanitizeHTML(summary))
	article.ContentHTML = template.HTML(sanitizeHTML(firstNonEmpty(content, summary)))
	article.IsRead = read == 1
	article.IsFavorite = favorite == 1
	return &article, nil
}

func scanArticles(rows *sql.Rows) []Article {
	var articles []Article
	for rows.Next() {
		var a Article
		var summary, content string
		var read, favorite int
		if err := rows.Scan(&a.ID, &a.FeedID, &a.FeedTitle, &a.Title, &a.URL, &a.Author, &summary, &content, &a.PublishedAt, &a.CreatedAt, &read, &favorite); err == nil {
			a.SummaryHTML = template.HTML(sanitizeHTML(summary))
			a.ContentHTML = template.HTML(sanitizeHTML(firstNonEmpty(content, summary)))
			a.IsRead = read == 1
			a.IsFavorite = favorite == 1
			articles = append(articles, a)
		}
	}
	return articles
}

func (a *App) fetchFeed(feedURL string) (*parsedFeed, error) {
	u, err := url.Parse(feedURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("enter a full http or https RSS URL")
	}
	req, err := http.NewRequest(http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SpeedRSS/1.0")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("feed returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBody))
	if err != nil {
		return nil, err
	}
	if feed, err := parseRSS(body); err == nil && len(feed.Items) > 0 {
		return feed, nil
	}
	if feed, err := parseAtom(body); err == nil && len(feed.Items) > 0 {
		return feed, nil
	}
	return nil, errors.New("no RSS or Atom entries found")
}

func (a *App) saveItems(feedID int64, items []parsedItem) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range items {
		title := strings.TrimSpace(html.UnescapeString(item.Title))
		if title == "" {
			title = "(untitled)"
		}
		itemURL := normalizeURL(item.URL)
		guid := strings.TrimSpace(item.GUID)
		if guid == "" {
			guid = itemURL
		}
		if guid == "" {
			guid = strings.ToLower(title)
		}
		summary := sanitizeHTML(item.SummaryHTML)
		content := sanitizeHTML(item.ContentHTML)
		res, err := tx.Exec(`INSERT OR IGNORE INTO articles (feed_id, guid, url, title, author, summary_html, content_html, published_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, feedID, guid, itemURL, title, strings.TrimSpace(item.Author), summary, content, nullableTime(item.PublishedAt))
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if id == 0 {
			_ = tx.QueryRow(`SELECT id FROM articles WHERE feed_id = ? AND guid = ?`, feedID, guid).Scan(&id)
			_, _ = tx.Exec(`UPDATE articles SET url = ?, title = ?, author = ?, summary_html = ?, content_html = ?, published_at = COALESCE(?, published_at) WHERE id = ?`,
				itemURL, title, strings.TrimSpace(item.Author), summary, content, nullableTime(item.PublishedAt), id)
		} else {
			_, _ = tx.Exec(`INSERT OR IGNORE INTO article_state (article_id) VALUES (?)`, id)
		}
		if id > 0 {
			body := stripTags(summary + " " + content)
			_, _ = tx.Exec(`INSERT INTO article_search (article_id, title, author, body) VALUES (?, ?, ?, ?)
				ON CONFLICT(article_id) DO UPDATE SET title = excluded.title, author = excluded.author, body = excluded.body`,
				id, title, strings.TrimSpace(item.Author), body)
		}
	}
	return tx.Commit()
}

func parseRSS(body []byte) (*parsedFeed, error) {
	var doc struct {
		Channel struct {
			Title       string `xml:"title"`
			Description string `xml:"description"`
			Link        string `xml:"link"`
			Items       []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				GUID        string `xml:"guid"`
				Description string `xml:"description"`
				Content     string `xml:"encoded"`
				PubDate     string `xml:"pubDate"`
				Author      string `xml:"author"`
				Creator     string `xml:"creator"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	feed := &parsedFeed{Title: doc.Channel.Title, Description: doc.Channel.Description, SiteURL: doc.Channel.Link}
	for _, item := range doc.Channel.Items {
		feed.Items = append(feed.Items, parsedItem{
			GUID:        firstNonEmpty(item.GUID, item.Link),
			Title:       item.Title,
			URL:         item.Link,
			Author:      firstNonEmpty(item.Creator, item.Author),
			SummaryHTML: item.Description,
			ContentHTML: item.Content,
			PublishedAt: parseTime(item.PubDate),
		})
	}
	return feed, nil
}

func parseAtom(body []byte) (*parsedFeed, error) {
	var doc struct {
		Title    string `xml:"title"`
		Subtitle string `xml:"subtitle"`
		Links    []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
		Entries []struct {
			ID        string `xml:"id"`
			Title     string `xml:"title"`
			Summary   string `xml:"summary"`
			Content   string `xml:"content"`
			Updated   string `xml:"updated"`
			Published string `xml:"published"`
			Author    struct {
				Name string `xml:"name"`
			} `xml:"author"`
			Links []struct {
				Href string `xml:"href,attr"`
				Rel  string `xml:"rel,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	feed := &parsedFeed{Title: doc.Title, Description: doc.Subtitle, SiteURL: atomLink(doc.Links)}
	for _, entry := range doc.Entries {
		feed.Items = append(feed.Items, parsedItem{
			GUID:        firstNonEmpty(entry.ID, atomLink(entry.Links)),
			Title:       entry.Title,
			URL:         atomLink(entry.Links),
			Author:      entry.Author.Name,
			SummaryHTML: entry.Summary,
			ContentHTML: entry.Content,
			PublishedAt: parseTime(firstNonEmpty(entry.Published, entry.Updated)),
		})
	}
	return feed, nil
}

func atomLink(links []struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}) string {
	for _, link := range links {
		if link.Rel == "" || link.Rel == "alternate" {
			return link.Href
		}
	}
	if len(links) > 0 {
		return links[0].Href
	}
	return ""
}

func parseTime(value string) sql.NullTime {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullTime{}
	}
	layouts := []string{time.RFC1123Z, time.RFC1123, time.RFC3339, time.RFC3339Nano, "Mon, 02 Jan 2006 15:04:05 -0700", "2006-01-02T15:04:05-07:00"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return sql.NullTime{Time: t, Valid: true}
		}
	}
	return sql.NullTime{}
}

func nullableTime(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func totals(feeds []Feed) (int, int) {
	var unread, favs int
	for _, feed := range feeds {
		unread += feed.UnreadCount
		favs += feed.FavoriteCount
	}
	return unread, favs
}

func hashPassword(password string) (string, string, error) {
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return "", "", err
	}
	salt := base64.RawStdEncoding.EncodeToString(saltBytes)
	return salt, hex.EncodeToString(pbkdf2SHA256([]byte(password), saltBytes, passwordIters, passwordKeyLen)), nil
}

func checkPassword(password, salt, expected string) bool {
	saltBytes, err := base64.RawStdEncoding.DecodeString(salt)
	if err != nil {
		return false
	}
	got := hex.EncodeToString(pbkdf2SHA256([]byte(password), saltBytes, passwordIters, passwordKeyLen))
	return hmac.Equal([]byte(got), []byte(expected))
}

func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	hLen := 32
	numBlocks := (keyLen + hLen - 1) / hLen
	var dk []byte
	for block := 1; block <= numBlocks; block++ {
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iter; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

var (
	scriptRE   = regexp.MustCompile(`(?is)<\s*(script|style|iframe|object|embed)[^>]*>.*?<\s*/\s*(script|style|iframe|object|embed)\s*>`)
	eventAttr  = regexp.MustCompile(`(?i)\s+on[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	jsHrefAttr = regexp.MustCompile(`(?i)(href|src)\s*=\s*("[^"]*javascript:[^"]*"|'[^']*javascript:[^']*'|javascript:[^\s>]+)`)
	tagRE      = regexp.MustCompile(`(?s)<[^>]*>`)
)

func sanitizeHTML(value string) string {
	value = strings.TrimSpace(value)
	value = scriptRE.ReplaceAllString(value, "")
	value = eventAttr.ReplaceAllString(value, "")
	value = jsHrefAttr.ReplaceAllString(value, `$1="#"`)
	return value
}

func stripTags(value string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRE.ReplaceAllString(value, " "))), " ")
}

func normalizeURL(value string) string {
	value = strings.TrimSpace(html.UnescapeString(value))
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return value
	}
	u.Fragment = ""
	return u.String()
}

func siteFromFeedURL(feedURL string) string {
	u, err := url.Parse(feedURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func faviconURL(siteURL string) string {
	u, err := url.Parse(siteURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/favicon.ico"
}

func hostLabel(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func searchWhere(q string) (string, []any) {
	parts := strings.Fields(q)
	var clauses []string
	var args []any
	for _, part := range parts {
		part = strings.TrimSpace(strings.Trim(part, `"*'`))
		if part != "" {
			clauses = append(clauses, `(article_search.title LIKE ? OR article_search.author LIKE ? OR article_search.body LIKE ?)`)
			like := "%" + strings.ReplaceAll(part, "%", `\%`) + "%"
			args = append(args, like, like, like)
		}
	}
	if len(clauses) == 0 {
		return "1 = 1", nil
	}
	return strings.Join(clauses, " AND "), args
}

func (a *App) render(w http.ResponseWriter, name string, data PageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func (a *App) handleCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", mime.TypeByExtension(".css"))
	_, _ = io.WriteString(w, appCSS)
}

func parseTemplates() *template.Template {
	funcs := template.FuncMap{
		"date": func(t sql.NullTime) string {
			if !t.Valid {
				return ""
			}
			return t.Time.Format("Jan 2, 2006")
		},
		"timeAgo": func(t sql.NullTime, created time.Time) string {
			if t.Valid {
				return t.Time.Format("Jan 2, 2006")
			}
			return created.Format("Jan 2, 2006")
		},
		"active": func(a, b any) string {
			if fmt.Sprint(a) == fmt.Sprint(b) {
				return "is-active"
			}
			return ""
		},
		"safe": func(v template.HTML) template.HTML { return v },
	}
	t := template.Must(template.New("base").Funcs(funcs).Parse(baseTemplate))
	template.Must(t.Parse(authTemplates))
	template.Must(t.Parse(appTemplate))
	return t
}

const baseTemplate = `{{define "shell"}}
<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>{{.Title}}</title>
	<link rel="stylesheet" href="/static/app.css">
</head>
<body>
	{{template "body" .}}
</body>
</html>
{{end}}`

const authTemplates = `{{define "login"}}{{template "auth" .}}{{end}}
{{define "setup"}}{{template "auth" .}}{{end}}
{{define "auth"}}
<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>{{.Title}}</title>
	<link rel="stylesheet" href="/static/app.css">
</head>
<body class="auth-page">
	<main class="auth-card">
		<div class="brand-mark">S</div>
		<h1>{{if eq .Title "Log in"}}Welcome back{{else}}Set up SpeedRSS{{end}}</h1>
		<p class="muted">{{if eq .Title "Log in"}}Log in to your personal feed reader.{{else}}Create the local admin account for this reader.{{end}}</p>
		{{if .Error}}<p class="notice error">{{.Error}}</p>{{end}}
		<form method="post" class="stack">
			<label>Username <input name="username" autocomplete="username" required autofocus></label>
			<label>Password <input name="password" type="password" autocomplete="{{if eq .Title "Log in"}}current-password{{else}}new-password{{end}}" required></label>
			<button class="primary" type="submit">{{if eq .Title "Log in"}}Log in{{else}}Create reader{{end}}</button>
		</form>
	</main>
</body>
</html>
{{end}}`

const appTemplate = `{{define "app"}}
<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>SpeedRSS</title>
	<link rel="stylesheet" href="/static/app.css">
</head>
<body>
<div class="layout">
	<aside class="sidebar">
		<div class="sidebar-head">
			<a class="logo" href="/">SpeedRSS</a>
			<form method="post" action="/logout"><button class="ghost small" title="Log out">Logout</button></form>
		</div>
		<form method="post" action="/feeds" class="add-feed">
			<input name="feed_url" placeholder="Paste RSS or Atom URL" required>
			<button class="icon-btn" title="Add feed">+</button>
		</form>
		<div class="quick-links">
			<a class="{{active .View "all"}}" href="/">All <span>{{.UnreadTotal}}</span></a>
			<a class="{{active .View "unread"}}" href="/?view=unread">Unread <span>{{.UnreadTotal}}</span></a>
			<a class="{{active .View "favorites"}}" href="/?view=favorites">Favorites <span>{{.FavoriteTotal}}</span></a>
		</div>
		<nav class="feeds">
			{{range .Feeds}}
			<a class="feed {{if eq $.SelectedFeedID .ID}}is-active{{end}}" href="/?feed={{.ID}}">
				{{if .FaviconURL}}<img src="{{.FaviconURL}}" alt="" loading="lazy">{{else}}<span class="feed-fallback">{{slice .Title 0 1}}</span>{{end}}
				<span class="feed-text"><strong>{{.Title}}</strong><small>{{.DisplayHostname}}</small></span>
				{{if .UnreadCount}}<span class="badge">{{.UnreadCount}}</span>{{end}}
			</a>
			{{if .LastError}}<p class="feed-error">{{.LastError.String}}</p>{{end}}
			{{else}}
			<p class="empty">Add your first feed to start reading.</p>
			{{end}}
		</nav>
		<a class="backup" href="/backup">Download data backup</a>
	</aside>
	<main class="main">
		<header class="topbar">
			<form class="search" method="get" action="/">
				{{if .SelectedFeedID}}<input type="hidden" name="feed" value="{{.SelectedFeedID}}">{{end}}
				{{if .View}}<input type="hidden" name="view" value="{{.View}}">{{end}}
				<input name="q" value="{{.Query}}" placeholder="Search everything">
			</form>
			<form method="post" action="/refresh"><button class="secondary">Refresh all</button></form>
			{{if .SelectedFeedID}}<form method="post" action="/feeds/{{.SelectedFeedID}}/refresh"><button class="secondary">Refresh feed</button></form>{{end}}
		</header>
		{{if .Error}}<p class="notice error">{{.Error}}</p>{{end}}
		{{if .Message}}<p class="notice">{{.Message}}</p>{{end}}
		<section class="content-grid">
			<div class="article-list">
				{{range .Articles}}
				<a class="article-row {{if not .IsRead}}unread{{end}} {{if $.SelectedArticle}}{{if eq $.SelectedArticle.ID .ID}}is-active{{end}}{{end}}" href="/articles/{{.ID}}?mark=read{{if $.Query}}&q={{$.Query}}{{end}}">
					<span class="unread-dot"></span>
					<span>
						<strong>{{.Title}}</strong>
						<small>{{.FeedTitle}} · {{timeAgo .PublishedAt .CreatedAt}}{{if .IsFavorite}} · Favorite{{end}}</small>
					</span>
				</a>
				{{else}}
				<p class="empty spacious">No articles here yet.</p>
				{{end}}
			</div>
			<article class="reader">
				{{if .SelectedArticle}}
				<div class="reader-head">
					<div>
						<p class="eyebrow">{{.SelectedArticle.FeedTitle}}</p>
						<h1>{{.SelectedArticle.Title}}</h1>
						<p class="muted">{{timeAgo .SelectedArticle.PublishedAt .SelectedArticle.CreatedAt}}{{if .SelectedArticle.Author}} · {{.SelectedArticle.Author}}{{end}}</p>
					</div>
					<div class="reader-actions">
						<a class="secondary" href="{{.SelectedArticle.URL}}" target="_blank" rel="noreferrer">Open original</a>
						<form method="post" action="/articles/{{.SelectedArticle.ID}}/read">
							<input type="hidden" name="read" value="{{if .SelectedArticle.IsRead}}false{{else}}true{{end}}">
							<button class="secondary">{{if .SelectedArticle.IsRead}}Mark unread{{else}}Mark read{{end}}</button>
						</form>
						<form method="post" action="/articles/{{.SelectedArticle.ID}}/favorite">
							<input type="hidden" name="favorite" value="{{if .SelectedArticle.IsFavorite}}false{{else}}true{{end}}">
							<button class="secondary">{{if .SelectedArticle.IsFavorite}}Unfavorite{{else}}Favorite{{end}}</button>
						</form>
					</div>
				</div>
				<div class="article-body">{{safe .SelectedArticle.ContentHTML}}</div>
				{{else}}
				<div class="reader-empty">
					<h1>Select an article</h1>
					<p class="muted">Unread posts get a dot. Open any article to mark it read, favorite it, or jump to the original blog.</p>
				</div>
				{{end}}
			</article>
		</section>
	</main>
</div>
</body>
</html>
{{end}}`

const appCSS = `
:root {
	--bg: #f7f7f4;
	--panel: #ffffff;
	--ink: #1f2933;
	--muted: #687481;
	--line: #dfe3e6;
	--accent: #2364aa;
	--accent-ink: #ffffff;
	--danger: #b42318;
	--soft: #eef3f7;
	--favorite: #b7791f;
}
* { box-sizing: border-box; }
html, body { margin: 0; min-height: 100%; }
body {
	background: var(--bg);
	color: var(--ink);
	font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
	font-size: 15px;
	line-height: 1.5;
}
a { color: inherit; text-decoration: none; }
button, input { font: inherit; }
input {
	width: 100%;
	border: 1px solid var(--line);
	border-radius: 7px;
	padding: 10px 11px;
	background: #fff;
	color: var(--ink);
}
button, .secondary, .primary, .ghost {
	border: 1px solid var(--line);
	border-radius: 7px;
	padding: 9px 12px;
	background: #fff;
	color: var(--ink);
	cursor: pointer;
	white-space: nowrap;
}
.primary { background: var(--accent); border-color: var(--accent); color: var(--accent-ink); width: 100%; }
.secondary:hover, .ghost:hover, button:hover { border-color: #b8c1ca; }
.small { padding: 6px 8px; font-size: 12px; }
.icon-btn {
	width: 42px;
	height: 42px;
	display: grid;
	place-items: center;
	font-size: 22px;
}
.layout {
	display: grid;
	grid-template-columns: 300px 1fr;
	min-height: 100vh;
}
.sidebar {
	background: #fbfbf9;
	border-right: 1px solid var(--line);
	padding: 18px;
	display: flex;
	flex-direction: column;
	gap: 16px;
	min-width: 0;
}
.sidebar-head, .topbar, .reader-actions, .add-feed {
	display: flex;
	align-items: center;
	gap: 10px;
}
.sidebar-head { justify-content: space-between; }
.logo { font-size: 20px; font-weight: 760; letter-spacing: 0; }
.add-feed input { min-width: 0; }
.quick-links {
	display: grid;
	gap: 4px;
}
.quick-links a {
	display: flex;
	justify-content: space-between;
	border-radius: 7px;
	padding: 8px 10px;
	color: var(--muted);
}
.quick-links a.is-active, .quick-links a:hover { background: var(--soft); color: var(--ink); }
.feeds {
	display: grid;
	align-content: start;
	gap: 5px;
	overflow: auto;
}
.feed {
	display: grid;
	grid-template-columns: 24px 1fr auto;
	align-items: center;
	gap: 10px;
	border-radius: 7px;
	padding: 8px;
	min-width: 0;
}
.feed:hover, .feed.is-active { background: var(--soft); }
.feed img, .feed-fallback {
	width: 24px;
	height: 24px;
	border-radius: 5px;
	background: #e8ecef;
	object-fit: contain;
	display: grid;
	place-items: center;
	font-size: 12px;
	font-weight: 700;
}
.feed-text { min-width: 0; }
.feed-text strong, .feed-text small {
	display: block;
	overflow: hidden;
	text-overflow: ellipsis;
	white-space: nowrap;
}
.feed-text small, .muted, .empty { color: var(--muted); }
.badge {
	min-width: 24px;
	padding: 2px 7px;
	border-radius: 999px;
	background: var(--accent);
	color: #fff;
	font-size: 12px;
	text-align: center;
}
.feed-error {
	color: var(--danger);
	font-size: 12px;
	margin: -2px 8px 6px 42px;
}
.backup {
	margin-top: auto;
	color: var(--muted);
	font-size: 13px;
}
.main { min-width: 0; }
.topbar {
	position: sticky;
	top: 0;
	z-index: 2;
	background: rgba(247, 247, 244, .94);
	backdrop-filter: blur(10px);
	border-bottom: 1px solid var(--line);
	padding: 14px 18px;
}
.search { flex: 1; }
.content-grid {
	display: grid;
	grid-template-columns: minmax(300px, 420px) minmax(0, 1fr);
	height: calc(100vh - 71px);
}
.article-list {
	border-right: 1px solid var(--line);
	overflow: auto;
	background: #fff;
}
.article-row {
	display: grid;
	grid-template-columns: 10px 1fr;
	gap: 10px;
	padding: 14px 16px;
	border-bottom: 1px solid var(--line);
	min-width: 0;
}
.article-row:hover, .article-row.is-active { background: #f3f6f8; }
.article-row strong {
	display: block;
	font-weight: 650;
	line-height: 1.35;
}
.article-row small {
	display: block;
	margin-top: 4px;
	color: var(--muted);
	overflow: hidden;
	text-overflow: ellipsis;
	white-space: nowrap;
}
.unread-dot {
	width: 8px;
	height: 8px;
	margin-top: 7px;
	border-radius: 50%;
	background: transparent;
}
.article-row.unread .unread-dot { background: var(--accent); }
.article-row.unread strong { font-weight: 780; }
.reader {
	overflow: auto;
	padding: 34px min(6vw, 72px);
	background: var(--panel);
}
.reader-head {
	display: flex;
	justify-content: space-between;
	gap: 24px;
	align-items: flex-start;
	border-bottom: 1px solid var(--line);
	padding-bottom: 22px;
	margin-bottom: 26px;
}
.reader h1 {
	font-size: clamp(28px, 3vw, 42px);
	line-height: 1.08;
	margin: 0;
	letter-spacing: 0;
}
.eyebrow {
	margin: 0 0 8px;
	color: var(--accent);
	font-weight: 760;
	font-size: 13px;
	text-transform: uppercase;
	letter-spacing: 0;
}
.reader-actions {
	flex-wrap: wrap;
	justify-content: flex-end;
}
.article-body {
	max-width: 780px;
	font-size: 18px;
	line-height: 1.72;
}
.article-body img {
	max-width: 100%;
	height: auto;
	border-radius: 7px;
}
.article-body pre, .article-body code {
	white-space: pre-wrap;
	background: #f1f3f5;
	border-radius: 6px;
	padding: 2px 5px;
}
.article-body blockquote {
	border-left: 3px solid var(--line);
	margin-left: 0;
	padding-left: 18px;
	color: #4b5563;
}
.reader-empty {
	min-height: 55vh;
	display: grid;
	place-content: center;
	text-align: center;
}
.notice {
	margin: 12px 18px 0;
	padding: 10px 12px;
	border-radius: 7px;
	background: #edf7ed;
	color: #225c2f;
}
.notice.error { background: #fff1f0; color: var(--danger); }
.spacious { padding: 24px; }
.auth-page {
	min-height: 100vh;
	display: grid;
	place-items: center;
	padding: 24px;
}
.auth-card {
	width: min(420px, 100%);
	background: #fff;
	border: 1px solid var(--line);
	border-radius: 8px;
	padding: 28px;
	box-shadow: 0 20px 45px rgba(31, 41, 51, .08);
}
.brand-mark {
	width: 42px;
	height: 42px;
	border-radius: 8px;
	background: var(--accent);
	color: #fff;
	display: grid;
	place-items: center;
	font-weight: 800;
}
.auth-card h1 { margin: 18px 0 4px; }
.stack { display: grid; gap: 14px; margin-top: 22px; }
.stack label { display: grid; gap: 6px; font-weight: 650; }
@media (max-width: 900px) {
	.layout { grid-template-columns: 1fr; }
	.sidebar { border-right: 0; border-bottom: 1px solid var(--line); max-height: 45vh; }
	.content-grid { grid-template-columns: 1fr; height: auto; }
	.article-list { max-height: 36vh; border-right: 0; }
	.reader { min-height: 55vh; padding: 24px 18px; }
	.reader-head { display: block; }
	.reader-actions { justify-content: flex-start; margin-top: 16px; }
	.topbar { flex-wrap: wrap; }
}
`
