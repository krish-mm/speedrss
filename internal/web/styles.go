package web

const CSS = `
:root {
	--bg: #eef0eb;
	--rail: #171a1f;
	--rail-soft: #23272f;
	--panel: #fbfbf8;
	--paper: #ffffff;
	--ink: #151922;
	--muted: #69717f;
	--line: #e0e3df;
	--line-strong: #c8cec8;
	--accent: #2f6f4e;
	--accent-ink: #ffffff;
	--warn: #b42318;
	--gold: #a16207;
}
* { box-sizing: border-box; }
html, body { margin: 0; min-height: 100%; }
body {
	background: var(--bg);
	color: var(--ink);
	font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
	font-size: 14px;
	line-height: 1.5;
}
a { color: inherit; text-decoration: none; }
button, input { font: inherit; }
input {
	width: 100%;
	border: 1px solid var(--line);
	border-radius: 8px;
	padding: 10px 12px;
	background: #fff;
	color: var(--ink);
	outline: none;
}
input:focus { border-color: var(--accent); box-shadow: 0 0 0 3px rgba(47, 111, 78, .12); }
.btn {
	border: 1px solid var(--line-strong);
	border-radius: 8px;
	background: #fff;
	color: var(--ink);
	padding: 9px 12px;
	cursor: pointer;
	display: inline-flex;
	align-items: center;
	justify-content: center;
	min-height: 38px;
	white-space: nowrap;
}
.btn:hover { border-color: #99a39b; background: #f7f8f5; }
.btn.primary { border-color: var(--accent); background: var(--accent); color: var(--accent-ink); }
.icon-button {
	width: 40px;
	height: 40px;
	border: 0;
	border-radius: 8px;
	background: var(--accent);
	color: #fff;
	font-size: 22px;
	cursor: pointer;
}
.app-shell {
	display: grid;
	grid-template-columns: 310px minmax(0, 1fr);
	min-height: 100vh;
}
.rail {
	background: var(--rail);
	color: #f5f6f3;
	padding: 18px;
	display: flex;
	flex-direction: column;
	gap: 16px;
	min-width: 0;
}
.brand, .auth-brand {
	display: flex;
	align-items: center;
	gap: 12px;
}
.brand strong, .auth-brand strong { display: block; font-size: 16px; }
.brand span, .auth-brand span { display: block; color: #9da6b3; font-size: 12px; }
.mark {
	width: 38px;
	height: 38px;
	border-radius: 8px;
	display: grid;
	place-items: center;
	background: var(--accent);
	color: #fff;
	font-weight: 800;
}
.add-feed { display: grid; grid-template-columns: 1fr 40px; gap: 8px; }
.add-feed input { background: var(--rail-soft); color: #fff; border-color: #343a45; }
.views { display: grid; gap: 4px; }
.views a {
	display: flex;
	justify-content: space-between;
	align-items: center;
	padding: 9px 10px;
	border-radius: 8px;
	color: #c8ced6;
}
.views a:hover, .views a.is-active { background: var(--rail-soft); color: #fff; }
.feed-section-title {
	color: #8d96a3;
	text-transform: uppercase;
	font-size: 11px;
	font-weight: 800;
	letter-spacing: .08em;
	margin-top: 4px;
}
.feeds { display: grid; gap: 6px; overflow: auto; padding-right: 2px; }
.feed {
	display: grid;
	grid-template-columns: 30px 1fr auto;
	align-items: center;
	gap: 10px;
	padding: 8px;
	border-radius: 8px;
	color: #dce1e7;
}
.feed:hover, .feed.is-active { background: var(--rail-soft); color: #fff; }
.feed-icon, .thumb.placeholder {
	width: 30px;
	height: 30px;
	border-radius: 7px;
	display: grid;
	place-items: center;
	background: #3a414c;
	color: #fff;
	font-size: 12px;
	font-weight: 800;
	overflow: hidden;
}
.feed-icon img { width: 100%; height: 100%; object-fit: contain; background: #fff; }
.feed-copy { min-width: 0; }
.feed-copy strong, .feed-copy small {
	display: block;
	overflow: hidden;
	text-overflow: ellipsis;
	white-space: nowrap;
}
.feed-copy small { color: #8d96a3; }
.count {
	min-width: 24px;
	padding: 2px 7px;
	border-radius: 999px;
	background: #e7f1e9;
	color: var(--accent);
	font-size: 12px;
	text-align: center;
	font-weight: 750;
}
.feed-log {
	margin: -2px 8px 4px 48px;
	color: #ffb4ab;
	font-size: 12px;
}
.rail-footer {
	margin-top: auto;
	padding-top: 10px;
	border-top: 1px solid #303641;
	display: flex;
	justify-content: space-between;
	gap: 12px;
	color: #aeb6c1;
	font-size: 13px;
}
.link-button { padding: 0; border: 0; background: transparent; color: inherit; cursor: pointer; }
.workspace { min-width: 0; display: flex; flex-direction: column; }
.toolbar {
	height: 68px;
	display: flex;
	align-items: center;
	gap: 12px;
	padding: 14px 18px;
	border-bottom: 1px solid var(--line);
	background: rgba(251, 251, 248, .94);
	backdrop-filter: blur(12px);
}
.search { flex: 1; }
.toolbar-actions { display: flex; gap: 8px; }
.reader-grid {
	display: grid;
	grid-template-columns: minmax(340px, 440px) minmax(0, 1fr);
	height: calc(100vh - 68px);
	min-height: 0;
}
.article-list {
	background: var(--panel);
	border-right: 1px solid var(--line);
	overflow: auto;
	padding: 12px;
	display: grid;
	align-content: start;
	gap: 10px;
}
.article-card {
	display: grid;
	grid-template-columns: 82px 1fr;
	gap: 12px;
	padding: 10px;
	border: 1px solid transparent;
	border-radius: 8px;
	background: #fff;
	box-shadow: 0 1px 2px rgba(17, 24, 39, .04);
}
.article-card:hover, .article-card.is-active {
	border-color: var(--line-strong);
	box-shadow: 0 8px 20px rgba(17, 24, 39, .07);
}
.article-card.unread { border-left: 4px solid var(--accent); }
.thumb {
	width: 82px;
	height: 62px;
	border-radius: 7px;
	object-fit: cover;
	background: #dfe5df;
}
.article-copy { min-width: 0; }
.article-copy strong {
	display: -webkit-box;
	-webkit-line-clamp: 2;
	-webkit-box-orient: vertical;
	overflow: hidden;
	line-height: 1.25;
	font-size: 15px;
}
.article-card.unread strong { font-weight: 800; }
.article-copy small {
	display: -webkit-box;
	-webkit-line-clamp: 2;
	-webkit-box-orient: vertical;
	overflow: hidden;
	margin-top: 5px;
	color: var(--muted);
	font-size: 12px;
}
.meta {
	display: block;
	color: var(--muted);
	font-size: 11px;
	margin-bottom: 4px;
	overflow: hidden;
	text-overflow: ellipsis;
	white-space: nowrap;
}
.dot {
	display: inline-block;
	width: 7px;
	height: 7px;
	border-radius: 50%;
	margin-right: 5px;
	background: transparent;
}
.unread .dot { background: var(--accent); }
.reader-pane {
	background: var(--paper);
	overflow: auto;
	padding: 34px min(5vw, 70px);
}
.reader-top {
	display: flex;
	justify-content: space-between;
	align-items: flex-start;
	gap: 26px;
	padding-bottom: 22px;
	border-bottom: 1px solid var(--line);
	margin-bottom: 28px;
}
.source-label {
	color: var(--accent);
	font-size: 12px;
	text-transform: uppercase;
	letter-spacing: .08em;
	font-weight: 850;
	margin-bottom: 8px;
}
.reader-top h1 {
	margin: 0;
	font-size: clamp(30px, 3vw, 48px);
	line-height: 1.06;
	letter-spacing: 0;
}
.reader-top p { margin: 10px 0 0; color: var(--muted); }
.reader-actions {
	display: flex;
	gap: 8px;
	flex-wrap: wrap;
	justify-content: flex-end;
}
.article-body {
	max-width: 820px;
	font-size: 18px;
	line-height: 1.75;
	color: #20242d;
}
.article-body img {
	display: block;
	max-width: 100%;
	height: auto;
	border-radius: 8px;
	margin: 20px 0;
}
.article-body a { color: var(--accent); text-decoration: underline; text-underline-offset: 3px; }
.article-body pre {
	overflow: auto;
	background: #f1f3f0;
	padding: 14px;
	border-radius: 8px;
}
.article-body blockquote {
	border-left: 3px solid var(--accent);
	margin-left: 0;
	padding-left: 18px;
	color: #4b5563;
}
.reader-empty, .empty-state {
	min-height: 280px;
	display: grid;
	place-content: center;
	text-align: center;
	color: var(--muted);
}
.empty-state.compact { min-height: 80px; color: #9da6b3; font-size: 13px; }
.empty-illustration {
	width: 72px;
	height: 72px;
	border-radius: 18px;
	background: #e7efe8;
	color: var(--accent);
	display: grid;
	place-items: center;
	margin: 0 auto 16px;
	font-weight: 850;
}
.toast {
	margin: 12px 18px 0;
	padding: 10px 12px;
	border-radius: 8px;
	background: #e8f3eb;
	color: #1f5f3d;
}
.toast.error { background: #fff1f0; color: var(--warn); }
.auth-screen {
	min-height: 100vh;
	display: grid;
	place-items: center;
	padding: 24px;
	background:
		linear-gradient(135deg, rgba(47,111,78,.10), transparent 34%),
		var(--bg);
}
.auth-panel {
	width: min(430px, 100%);
	background: #fff;
	border: 1px solid var(--line);
	border-radius: 8px;
	padding: 30px;
	box-shadow: 0 24px 60px rgba(17, 24, 39, .12);
}
.auth-panel h1 { margin: 26px 0 6px; font-size: 34px; line-height: 1.05; }
.auth-panel p { margin: 0; color: var(--muted); }
.auth-form { display: grid; gap: 14px; margin-top: 24px; }
.auth-form label { display: grid; gap: 6px; font-weight: 700; }
@media (max-width: 980px) {
	.app-shell { grid-template-columns: 1fr; }
	.rail { max-height: 46vh; }
	.reader-grid { grid-template-columns: 1fr; height: auto; }
	.article-list { max-height: 42vh; border-right: 0; }
	.reader-pane { min-height: 56vh; padding: 24px 18px; }
	.reader-top { display: block; }
	.reader-actions { justify-content: flex-start; margin-top: 16px; }
	.toolbar { height: auto; flex-wrap: wrap; }
	.toolbar-actions { width: 100%; }
}
`
