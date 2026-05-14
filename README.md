# SpeedRSS

A small self-hosted RSS reader written in Go.

## Features

- Single-user login with first-run setup
- Add and refresh RSS/Atom feeds
- Feed sidebar with favicon support
- Unread/read tracking
- Favorites
- Article reader with images preserved from the feed content
- Open-original button for the source blog URL
- Global search across stored articles
- Portable `data/` folder backed by SQLite

## Run

```sh
go run .
```

Then open [http://localhost:8080](http://localhost:8080).

The app stores all state in `data/speedrss.db`. Back up or move the full `data/` folder to restore your feeds, articles, read state, favorites, sessions, and cached metadata.

## Backup and restore

Use the in-app "Download data backup" link to download a zip of the `data/` folder.

To restore a backup before starting the app:

```sh
go run . -restore speedrss-data.zip
go run .
```

Restoring moves the previous `data/` folder aside as `data.old-YYYYMMDDHHMMSS` and replaces it with the uploaded backup contents.
