package store

import (
	"database/sql"
	"html/template"
	"time"
)

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
	PreviewText string
	ImageURL    string
	PublishedAt sql.NullTime
	CreatedAt   time.Time
	IsRead      bool
	IsFavorite  bool
}

type ArticleInput struct {
	GUID        string
	Title       string
	URL         string
	Author      string
	SummaryHTML string
	ContentHTML string
	ImageURL    string
	PublishedAt sql.NullTime
}
