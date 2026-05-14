package web

import (
	"database/sql"
	"fmt"
	"html/template"
	"strings"
	"time"
)

func Templates() *template.Template {
	funcs := template.FuncMap{
		"date": func(t sql.NullTime, created time.Time) string {
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
		"initial": func(s string) string {
			s = strings.TrimSpace(s)
			if s == "" {
				return "?"
			}
			return strings.ToUpper(string([]rune(s)[0]))
		},
		"safe": func(v template.HTML) template.HTML { return v },
	}
	t := template.Must(template.New("speedrss").Funcs(funcs).Parse(authTemplates))
	template.Must(t.Parse(appTemplate))
	return t
}

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
<body class="auth-screen">
	<main class="auth-panel">
		<div class="auth-brand">
			<div class="mark">S</div>
			<div><strong>SpeedRSS</strong><span>Local-first feed reader</span></div>
		</div>
		<h1>{{if eq .Title "Log in"}}Welcome back{{else}}Create your reader{{end}}</h1>
		<p>{{if eq .Title "Log in"}}Log in to continue reading.{{else}}Set up the admin account stored in your data folder.{{end}}</p>
		{{if .Error}}<div class="toast error">{{.Error}}</div>{{end}}
		<form method="post" class="auth-form">
			<label>Username<input name="username" autocomplete="username" required autofocus></label>
			<label>Password<input name="password" type="password" autocomplete="{{if eq .Title "Log in"}}current-password{{else}}new-password{{end}}" required></label>
			<button class="btn primary" type="submit">{{if eq .Title "Log in"}}Log in{{else}}Create account{{end}}</button>
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
<div class="app-shell">
	<aside class="rail">
		<div class="brand">
			<div class="mark">S</div>
			<div><strong>SpeedRSS</strong><span>{{.UnreadTotal}} unread</span></div>
		</div>
		<form method="post" action="/feeds" class="add-feed">
			<input name="feed_url" placeholder="Add RSS / Atom URL" required>
			<button class="icon-button" title="Add feed">+</button>
		</form>
		<nav class="views">
			<a class="{{active .View "all"}}" href="/">All articles <span>{{.ArticleTotal}}</span></a>
			<a class="{{active .View "unread"}}" href="/?view=unread">Unread <span>{{.UnreadTotal}}</span></a>
			<a class="{{active .View "favorites"}}" href="/?view=favorites">Favorites <span>{{.FavoriteTotal}}</span></a>
		</nav>
		<div class="feed-section-title">Feeds</div>
		<nav class="feeds">
			{{range .Feeds}}
			<a class="feed {{if eq $.SelectedFeedID .ID}}is-active{{end}}" href="/?feed={{.ID}}">
				<span class="feed-icon">{{if .FaviconURL}}<img src="{{.FaviconURL}}" alt="" loading="lazy" referrerpolicy="no-referrer">{{else}}{{initial .Title}}{{end}}</span>
				<span class="feed-copy"><strong>{{.Title}}</strong><small>{{.DisplayHostname}}</small></span>
				{{if .UnreadCount}}<span class="count">{{.UnreadCount}}</span>{{end}}
			</a>
			{{if .LastError.Valid}}<div class="feed-log">Refresh failed: {{.LastError.String}}</div>{{end}}
			{{else}}
			<div class="empty-state compact">No feeds yet. Paste a feed URL above.</div>
			{{end}}
		</nav>
		<div class="rail-footer">
			<a href="/backup">Download backup</a>
			<form method="post" action="/logout"><button class="link-button">Log out</button></form>
		</div>
	</aside>

	<main class="workspace">
		<header class="toolbar">
			<form class="search" method="get" action="/">
				{{if .SelectedFeedID}}<input type="hidden" name="feed" value="{{.SelectedFeedID}}">{{end}}
				{{if .View}}<input type="hidden" name="view" value="{{.View}}">{{end}}
				<input name="q" value="{{.Query}}" placeholder="Search titles, authors, and content">
			</form>
			<div class="toolbar-actions">
				<form method="post" action="/refresh"><button class="btn">Refresh all</button></form>
				{{if .SelectedFeedID}}<form method="post" action="/feeds/{{.SelectedFeedID}}/refresh"><button class="btn">Refresh feed</button></form>{{end}}
			</div>
		</header>
		{{if .Error}}<div class="toast error">{{.Error}}</div>{{end}}
		<div class="reader-grid">
			<section class="article-list" aria-label="Articles">
				{{range .Articles}}
				<a class="article-card {{if not .IsRead}}unread{{end}} {{if $.SelectedArticle}}{{if eq $.SelectedArticle.ID .ID}}is-active{{end}}{{end}}" href="/articles/{{.ID}}?mark=read{{if $.Query}}&q={{$.Query}}{{end}}">
					{{if .ImageURL}}<img class="thumb" src="{{.ImageURL}}" alt="" loading="lazy" referrerpolicy="no-referrer">{{else}}<span class="thumb placeholder">{{initial .FeedTitle}}</span>{{end}}
					<span class="article-copy">
						<span class="meta"><span class="dot"></span>{{.FeedTitle}} · {{date .PublishedAt .CreatedAt}}{{if .IsFavorite}} · Favorite{{end}}</span>
						<strong>{{.Title}}</strong>
						{{if .PreviewText}}<small>{{.PreviewText}}</small>{{end}}
					</span>
				</a>
				{{else}}
				<div class="empty-state">No articles match this view.</div>
				{{end}}
			</section>

			<article class="reader-pane">
				{{if .SelectedArticle}}
				<div class="reader-top">
					<div>
						<div class="source-label">{{.SelectedArticle.FeedTitle}}</div>
						<h1>{{.SelectedArticle.Title}}</h1>
						<p>{{date .SelectedArticle.PublishedAt .SelectedArticle.CreatedAt}}{{if .SelectedArticle.Author}} · {{.SelectedArticle.Author}}{{end}}</p>
					</div>
					<div class="reader-actions">
						<a class="btn primary" href="{{.SelectedArticle.URL}}" target="_blank" rel="noreferrer">Open full blog</a>
						<form method="post" action="/articles/{{.SelectedArticle.ID}}/favorite">
							<input type="hidden" name="favorite" value="{{if .SelectedArticle.IsFavorite}}false{{else}}true{{end}}">
							<button class="btn">{{if .SelectedArticle.IsFavorite}}Favorited{{else}}Favorite{{end}}</button>
						</form>
						<form method="post" action="/articles/{{.SelectedArticle.ID}}/read">
							<input type="hidden" name="read" value="{{if .SelectedArticle.IsRead}}false{{else}}true{{end}}">
							<button class="btn">{{if .SelectedArticle.IsRead}}Mark unread{{else}}Mark read{{end}}</button>
						</form>
					</div>
				</div>
				<div class="article-body">{{safe .SelectedArticle.ContentHTML}}</div>
				{{else}}
				<div class="reader-empty">
					<div class="empty-illustration">RSS</div>
					<h1>Choose an article</h1>
					<p>Unread articles are highlighted in the list. Use “Open full blog” whenever a feed only provides an excerpt.</p>
				</div>
				{{end}}
			</article>
		</div>
	</main>
</div>
</body>
</html>
{{end}}`
