package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "data", "speedrss.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSaveArticlesSearchAndState(t *testing.T) {
	st := newTestStore(t)
	feedID, err := st.UpsertFeed("NVIDIA Technical Blog", "https://developer.nvidia.com/blog/feed", "https://developer.nvidia.com/blog", "News", "https://developer.nvidia.com/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	err = st.SaveArticles(feedID, []ArticleInput{{
		GUID:        "nvidia-1",
		Title:       "Vera Rubin Platform",
		URL:         "https://developer.nvidia.com/blog/full-post/",
		Author:      "Ada",
		SummaryHTML: `<p>Agentic inference</p>`,
		ContentHTML: `<p>Searchable body</p><img src="https://developer.nvidia.com/a.jpg">`,
		ImageURL:    "https://developer.nvidia.com/a.jpg",
		PublishedAt: sql.NullTime{Time: time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC), Valid: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	results := st.ListArticles(0, "Searchable", "all")
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if results[0].URL != "https://developer.nvidia.com/blog/full-post/" {
		t.Fatalf("full blog URL was not saved: %q", results[0].URL)
	}
	if results[0].ImageURL == "" || !strings.Contains(string(results[0].ContentHTML), "img") {
		t.Fatalf("image not available in article: %#v", results[0])
	}
	st.SetRead(results[0].ID, true)
	st.SetFavorite(results[0].ID, true)
	article, err := st.Article(results[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !article.IsRead || !article.IsFavorite {
		t.Fatalf("state not persisted: %#v", article)
	}
}
