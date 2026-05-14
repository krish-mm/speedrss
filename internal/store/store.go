package store

import (
	"database/sql"
	"errors"
	"html"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

func Open(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func (s *Store) Migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_salt TEXT NOT NULL, password_hash TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS sessions (token TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS feeds (id INTEGER PRIMARY KEY, title TEXT NOT NULL, feed_url TEXT NOT NULL UNIQUE, site_url TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '', favicon_url TEXT NOT NULL DEFAULT '', last_fetched_at DATETIME, last_error TEXT, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS articles (id INTEGER PRIMARY KEY, feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE, guid TEXT NOT NULL, url TEXT NOT NULL DEFAULT '', title TEXT NOT NULL, author TEXT NOT NULL DEFAULT '', summary_html TEXT NOT NULL DEFAULT '', content_html TEXT NOT NULL DEFAULT '', image_url TEXT NOT NULL DEFAULT '', published_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE(feed_id, guid))`,
		`CREATE TABLE IF NOT EXISTS article_state (article_id INTEGER PRIMARY KEY REFERENCES articles(id) ON DELETE CASCADE, is_read INTEGER NOT NULL DEFAULT 0, is_favorite INTEGER NOT NULL DEFAULT 0, read_at DATETIME, favorited_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS article_search (article_id INTEGER PRIMARY KEY REFERENCES articles(id) ON DELETE CASCADE, title TEXT NOT NULL, author TEXT NOT NULL, body TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_articles_feed_published ON articles(feed_id, published_at DESC, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	_, _ = s.db.Exec(`ALTER TABLE articles ADD COLUMN image_url TEXT NOT NULL DEFAULT ''`)
	return nil
}

func (s *Store) HasUsers() bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n > 0
}

func (s *Store) CreateUser(username, salt, hash string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO users (username, password_salt, password_hash) VALUES (?, ?, ?)`, username, salt, hash)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UserPassword(username string) (int64, string, string, error) {
	var id int64
	var salt, hash string
	err := s.db.QueryRow(`SELECT id, password_salt, password_hash FROM users WHERE username = ?`, username).Scan(&id, &salt, &hash)
	return id, salt, hash, err
}

func (s *Store) CreateSession(token string, userID int64, expires time.Time) error {
	_, err := s.db.Exec(`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`, token, userID, expires)
	return err
}

func (s *Store) DeleteSession(token string) {
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
}

func (s *Store) UserBySession(token string) (*User, time.Time, error) {
	var user User
	var expires time.Time
	err := s.db.QueryRow(`
		SELECT users.id, users.username, sessions.expires_at
		FROM sessions JOIN users ON users.id = sessions.user_id
		WHERE sessions.token = ?`, token).Scan(&user.ID, &user.Username, &expires)
	return &user, expires, err
}

func (s *Store) UpsertFeed(title, feedURL, siteURL, description, favicon string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO feeds (title, feed_url, site_url, description, favicon_url, last_fetched_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(feed_url) DO UPDATE SET title = excluded.title, site_url = excluded.site_url, description = excluded.description, favicon_url = excluded.favicon_url, last_error = NULL, last_fetched_at = CURRENT_TIMESTAMP`,
		title, feedURL, siteURL, description, favicon)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		_ = s.db.QueryRow(`SELECT id FROM feeds WHERE feed_url = ?`, feedURL).Scan(&id)
	}
	return id, nil
}

func (s *Store) FeedURL(id int64) (string, error) {
	var feedURL string
	err := s.db.QueryRow(`SELECT feed_url FROM feeds WHERE id = ?`, id).Scan(&feedURL)
	if err != nil {
		return "", errors.New("feed not found")
	}
	return feedURL, nil
}

func (s *Store) MarkFeedError(id int64, err error) {
	_, _ = s.db.Exec(`UPDATE feeds SET last_error = ? WHERE id = ?`, err.Error(), id)
}

func (s *Store) ListFeedIDs() []int64 {
	rows, err := s.db.Query(`SELECT id FROM feeds ORDER BY title`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Store) ListFeeds() []Feed {
	rows, err := s.db.Query(`
		SELECT feeds.id, feeds.title, feeds.site_url, feeds.feed_url, feeds.description, feeds.favicon_url, feeds.last_fetched_at, feeds.last_error,
			COALESCE(SUM(CASE WHEN COALESCE(article_state.is_read, 0) = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN COALESCE(article_state.is_favorite, 0) = 1 THEN 1 ELSE 0 END), 0),
			COUNT(articles.id)
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
		if rows.Scan(&f.ID, &f.Title, &f.SiteURL, &f.FeedURL, &f.Description, &f.FaviconURL, &f.LastFetchedAt, &f.LastError, &f.UnreadCount, &f.FavoriteCount, &f.TotalCount) == nil {
			f.DisplayHostname = HostLabel(FirstNonEmpty(f.SiteURL, f.FeedURL))
			feeds = append(feeds, f)
		}
	}
	return feeds
}

func (s *Store) SaveArticles(feedID int64, items []ArticleInput) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range items {
		title := strings.TrimSpace(html.UnescapeString(item.Title))
		if title == "" {
			title = "(untitled)"
		}
		link := NormalizeURL(item.URL)
		guid := FirstNonEmpty(item.GUID, link, strings.ToLower(title))
		summary := SanitizeHTML(item.SummaryHTML)
		content := SanitizeHTML(FirstNonEmpty(item.ContentHTML, item.SummaryHTML))
		res, err := tx.Exec(`INSERT OR IGNORE INTO articles (feed_id, guid, url, title, author, summary_html, content_html, image_url, published_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, feedID, guid, link, title, strings.TrimSpace(item.Author), summary, content, item.ImageURL, nullableTime(item.PublishedAt))
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if id == 0 {
			_ = tx.QueryRow(`SELECT id FROM articles WHERE feed_id = ? AND guid = ?`, feedID, guid).Scan(&id)
			_, _ = tx.Exec(`UPDATE articles SET url = ?, title = ?, author = ?, summary_html = ?, content_html = ?, image_url = ?, published_at = COALESCE(?, published_at) WHERE id = ?`,
				link, title, strings.TrimSpace(item.Author), summary, content, item.ImageURL, nullableTime(item.PublishedAt), id)
		} else {
			_, _ = tx.Exec(`INSERT OR IGNORE INTO article_state (article_id) VALUES (?)`, id)
		}
		if id > 0 {
			body := StripTags(summary + " " + content)
			_, _ = tx.Exec(`INSERT INTO article_search (article_id, title, author, body) VALUES (?, ?, ?, ?)
				ON CONFLICT(article_id) DO UPDATE SET title = excluded.title, author = excluded.author, body = excluded.body`,
				id, title, strings.TrimSpace(item.Author), body)
		}
	}
	return tx.Commit()
}

func (s *Store) ListArticles(feedID int64, query, view string) []Article {
	sqlText := baseArticleSQL()
	args := []any{}
	if query != "" {
		where, searchArgs := searchWhere(query)
		sqlText = strings.Replace(sqlText, "FROM articles", "FROM article_search JOIN articles ON articles.id = article_search.article_id", 1)
		sqlText += " AND " + where
		args = append(args, searchArgs...)
	}
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
	sqlText += ` ORDER BY COALESCE(articles.published_at, articles.created_at) DESC LIMIT 250`
	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanArticles(rows)
}

func (s *Store) Article(id int64) (*Article, error) {
	row := s.db.QueryRow(baseArticleSQL()+` AND articles.id = ?`, id)
	articles := scanArticleRows(row)
	if len(articles) == 0 {
		return nil, sql.ErrNoRows
	}
	return &articles[0], nil
}

func (s *Store) SetRead(id int64, read bool) {
	var readAt any
	if read {
		readAt = time.Now()
	}
	_, _ = s.db.Exec(`INSERT INTO article_state (article_id, is_read, read_at) VALUES (?, ?, ?)
		ON CONFLICT(article_id) DO UPDATE SET is_read = excluded.is_read, read_at = excluded.read_at`, id, boolInt(read), readAt)
}

func (s *Store) SetFavorite(id int64, favorite bool) {
	var favAt any
	if favorite {
		favAt = time.Now()
	}
	_, _ = s.db.Exec(`INSERT INTO article_state (article_id, is_favorite, favorited_at) VALUES (?, ?, ?)
		ON CONFLICT(article_id) DO UPDATE SET is_favorite = excluded.is_favorite, favorited_at = excluded.favorited_at`, id, boolInt(favorite), favAt)
}

func baseArticleSQL() string {
	return `SELECT articles.id, articles.feed_id, feeds.title, articles.title, articles.url, articles.author, articles.summary_html, articles.content_html, articles.image_url,
		articles.published_at, articles.created_at, COALESCE(article_state.is_read, 0), COALESCE(article_state.is_favorite, 0)
		FROM articles
		JOIN feeds ON feeds.id = articles.feed_id
		LEFT JOIN article_state ON article_state.article_id = articles.id
		WHERE 1 = 1`
}

type scanner interface{ Scan(dest ...any) error }

func scanArticleRows(row scanner) []Article {
	var a Article
	var summary, content string
	var read, favorite int
	if err := row.Scan(&a.ID, &a.FeedID, &a.FeedTitle, &a.Title, &a.URL, &a.Author, &summary, &content, &a.ImageURL, &a.PublishedAt, &a.CreatedAt, &read, &favorite); err != nil {
		return nil
	}
	a.SummaryHTML = template.HTML(SanitizeHTML(summary))
	a.ContentHTML = template.HTML(SanitizeHTML(FirstNonEmpty(content, summary)))
	a.PreviewText = preview(StripTags(FirstNonEmpty(summary, content)), 150)
	a.IsRead = read == 1
	a.IsFavorite = favorite == 1
	return []Article{a}
}

func scanArticles(rows *sql.Rows) []Article {
	var articles []Article
	for rows.Next() {
		if a := scanArticleRows(rows); len(a) == 1 {
			articles = append(articles, a[0])
		}
	}
	return articles
}

func nullableTime(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time
}

func searchWhere(q string) (string, []any) {
	var clauses []string
	var args []any
	for _, part := range strings.Fields(q) {
		part = strings.TrimSpace(strings.Trim(part, `"*'`))
		if part == "" {
			continue
		}
		clauses = append(clauses, `(article_search.title LIKE ? OR article_search.author LIKE ? OR article_search.body LIKE ?)`)
		like := "%" + strings.ReplaceAll(part, "%", `\%`) + "%"
		args = append(args, like, like, like)
	}
	if len(clauses) == 0 {
		return "1 = 1", nil
	}
	return strings.Join(clauses, " AND "), args
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

var (
	scriptRE   = regexp.MustCompile(`(?is)<\s*(script|style|iframe|object|embed)[^>]*>.*?<\s*/\s*(script|style|iframe|object|embed)\s*>`)
	eventAttr  = regexp.MustCompile(`(?i)\s+on[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	jsHrefAttr = regexp.MustCompile(`(?i)(href|src)\s*=\s*("[^"]*javascript:[^"]*"|'[^']*javascript:[^']*'|javascript:[^\s>]+)`)
	tagRE      = regexp.MustCompile(`(?s)<[^>]*>`)
	imgRE      = regexp.MustCompile(`(?is)<img[^>]+src=["']([^"']+)["']`)
)

func SanitizeHTML(value string) string {
	value = strings.TrimSpace(value)
	value = scriptRE.ReplaceAllString(value, "")
	value = eventAttr.ReplaceAllString(value, "")
	value = jsHrefAttr.ReplaceAllString(value, `$1="#"`)
	return value
}

func StripTags(value string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRE.ReplaceAllString(value, " "))), " ")
}

func FirstImage(value string) string {
	match := imgRE.FindStringSubmatch(value)
	if len(match) == 2 {
		return html.UnescapeString(match[1])
	}
	return ""
}

func NormalizeURL(value string) string {
	value = strings.TrimSpace(html.UnescapeString(value))
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return value
	}
	u.Fragment = ""
	return u.String()
}

func HostLabel(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func preview(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "..."
}
