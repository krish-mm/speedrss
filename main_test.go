package main

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "speedrss.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	app := &App{
		db:        db,
		templates: parseTemplates(),
		client:    http.DefaultClient,
	}
	if err := app.migrate(); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestParseRSSPreservesContentImages(t *testing.T) {
	feed, err := parseRSS([]byte(`<?xml version="1.0"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/">
	<channel>
		<title>Example Feed</title>
		<link>https://example.com</link>
		<description>News</description>
		<item>
			<title>Post One</title>
			<link>https://example.com/post-one</link>
			<guid>post-one</guid>
			<pubDate>Mon, 02 Jan 2006 15:04:05 -0700</pubDate>
			<content:encoded><![CDATA[<p>Hello</p><img src="https://example.com/image.jpg">]]></content:encoded>
		</item>
	</channel>
</rss>`))
	if err != nil {
		t.Fatal(err)
	}
	if feed.Title != "Example Feed" || len(feed.Items) != 1 {
		t.Fatalf("unexpected feed: %#v", feed)
	}
	if !strings.Contains(feed.Items[0].ContentHTML, "image.jpg") {
		t.Fatalf("content image was not preserved: %q", feed.Items[0].ContentHTML)
	}
	if !feed.Items[0].PublishedAt.Valid {
		t.Fatal("published time was not parsed")
	}
}

func TestSaveItemsReadFavoriteAndSearch(t *testing.T) {
	app := newTestApp(t)
	res, err := app.db.Exec(`INSERT INTO feeds (title, feed_url, site_url) VALUES (?, ?, ?)`, "Example", "https://example.com/feed.xml", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	feedID, _ := res.LastInsertId()

	err = app.saveItems(feedID, []parsedItem{{
		GUID:        "one",
		Title:       "SQLite backed RSS reader",
		URL:         "https://example.com/one",
		Author:      "Ada",
		SummaryHTML: `<p>A useful post</p>`,
		ContentHTML: `<p>Searchable article body with <img src="https://example.com/a.png"></p>`,
		PublishedAt: sql.NullTime{Time: time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC), Valid: true},
	}})
	if err != nil {
		t.Fatal(err)
	}

	articles := app.listArticles(0, "searchable", "all")
	if len(articles) != 1 {
		t.Fatalf("expected search result, got %d", len(articles))
	}
	if articles[0].IsRead {
		t.Fatal("new article should be unread")
	}

	_, err = app.db.Exec(`INSERT INTO article_state (article_id, is_read, is_favorite) VALUES (?, 1, 1)
		ON CONFLICT(article_id) DO UPDATE SET is_read = 1, is_favorite = 1`, articles[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	article, err := app.getArticle(articles[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !article.IsRead || !article.IsFavorite {
		t.Fatalf("state was not persisted: read=%v favorite=%v", article.IsRead, article.IsFavorite)
	}
	if !strings.Contains(string(article.ContentHTML), "img src") {
		t.Fatalf("article image markup missing: %s", article.ContentHTML)
	}
}

func TestDataFolderIsPortable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	dbFile := filepath.Join(dir, "data", "speedrss.db")
	db, err := sql.Open("sqlite3", dbFile)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{db: db, templates: parseTemplates(), client: http.DefaultClient}
	if err := app.migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.Exec(`INSERT INTO feeds (title, feed_url, site_url) VALUES ('Restored', 'https://example.com/rss', 'https://example.com')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	reopened, err := sql.Open("sqlite3", dbFile)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	app = &App{db: reopened, templates: parseTemplates(), client: http.DefaultClient}
	feeds := app.listFeeds()
	if len(feeds) != 1 || feeds[0].Title != "Restored" {
		t.Fatalf("portable data folder did not restore feed: %#v", feeds)
	}
}
