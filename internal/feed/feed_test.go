package feed

import (
	"strings"
	"testing"
)

func TestParseNVIDIAAtomFeed(t *testing.T) {
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
	<title type="text">NVIDIA Technical Blog</title>
	<subtitle type="text">News and tutorials for developers</subtitle>
	<link rel="alternate" type="text/html" href="https://developer.nvidia.com/blog" />
	<entry>
		<author><name>Graham Steele</name></author>
		<title type="html"><![CDATA[How the NVIDIA Vera Rubin Platform is Solving Agentic AI’s Scale-Up Problem]]></title>
		<link rel="alternate" type="text/html" href="https://developer.nvidia.com/blog/how-the-nvidia-vera-rubin-platform-is-solving-agentic-ais-scale-up-problem/" />
		<id>https://developer.nvidia.com/blog/?p=116892</id>
		<published>2026-05-14T19:24:35Z</published>
		<summary type="html"><![CDATA[<img src="https://developer-blogs.nvidia.com/image.jpg">Short summary]]></summary>
		<content type="html"><![CDATA[<img src="https://developer-blogs.nvidia.com/image.jpg"><p>Excerpt content</p>]]></content>
	</entry>
</feed>`)
	f, err := parseAtom(body)
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "NVIDIA Technical Blog" || f.SiteURL != "https://developer.nvidia.com/blog" {
		t.Fatalf("bad feed metadata: %#v", f)
	}
	if len(f.Items) != 1 {
		t.Fatalf("expected one item, got %d", len(f.Items))
	}
	item := f.Items[0]
	if !strings.Contains(item.URL, "how-the-nvidia-vera-rubin-platform") {
		t.Fatalf("article URL was not the full blog URL: %q", item.URL)
	}
	if item.ImageURL == "" || !strings.Contains(item.ContentHTML, "image.jpg") {
		t.Fatalf("image was not extracted/preserved: %#v", item)
	}
	if !item.PublishedAt.Valid {
		t.Fatal("published date was not parsed")
	}
}

func TestParseIconLinks(t *testing.T) {
	icons := parseIconLinks([]byte(`<html><head>
		<link rel="apple-touch-icon" href="/apple.png">
		<link rel="icon" href="https://example.com/favicon.svg">
	</head></html>`))
	if len(icons) != 2 || icons[0] != "/apple.png" || icons[1] != "https://example.com/favicon.svg" {
		t.Fatalf("unexpected icons: %#v", icons)
	}
}
