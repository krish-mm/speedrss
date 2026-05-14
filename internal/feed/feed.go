package feed

import (
	"bytes"
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"speedrss/internal/store"
)

const maxFeedBody = 20 << 20

type Client struct {
	HTTP *http.Client
}

type Feed struct {
	Title       string
	Description string
	SiteURL     string
	FaviconURL  string
	Items       []store.ArticleInput
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{
		Timeout: 25 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 6 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}}
}

func (c *Client) Fetch(feedURL string) (*Feed, error) {
	u, err := url.Parse(feedURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("enter a full http or https RSS URL")
	}
	body, err := c.get(feedURL, maxFeedBody)
	if err != nil {
		return nil, err
	}
	var f *Feed
	if parsed, err := parseAtom(body); err == nil && len(parsed.Items) > 0 {
		f = parsed
	} else if parsed, err := parseRSS(body); err == nil && len(parsed.Items) > 0 {
		f = parsed
	} else {
		return nil, errors.New("no RSS or Atom entries found")
	}
	if f.SiteURL == "" {
		f.SiteURL = feedOrigin(feedURL)
	}
	f.FaviconURL = c.DiscoverFavicon(f.SiteURL)
	if f.FaviconURL == "" {
		f.FaviconURL = feedOrigin(feedURL) + "/favicon.ico"
	}
	return f, nil
}

func (c *Client) get(rawurl string, limit int64) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SpeedRSS/1.0 (+https://localhost)")
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml, text/html;q=0.8, */*;q=0.5")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("request returned %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (c *Client) DiscoverFavicon(siteURL string) string {
	if siteURL == "" {
		return ""
	}
	body, err := c.get(siteURL, 2<<20)
	if err != nil {
		return siteURL + "/favicon.ico"
	}
	for _, icon := range parseIconLinks(body) {
		if absolute := resolveURL(siteURL, icon); absolute != "" {
			return absolute
		}
	}
	return siteURL + "/favicon.ico"
}

func parseRSS(body []byte) (*Feed, error) {
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
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = false
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	f := &Feed{Title: html.UnescapeString(doc.Channel.Title), Description: doc.Channel.Description, SiteURL: store.NormalizeURL(doc.Channel.Link)}
	for _, item := range doc.Channel.Items {
		content := store.FirstNonEmpty(item.Content, item.Description)
		f.Items = append(f.Items, store.ArticleInput{
			GUID:        store.FirstNonEmpty(item.GUID, item.Link),
			Title:       html.UnescapeString(item.Title),
			URL:         item.Link,
			Author:      store.FirstNonEmpty(item.Creator, item.Author),
			SummaryHTML: item.Description,
			ContentHTML: content,
			ImageURL:    store.FirstImage(content),
			PublishedAt: parseTime(item.PubDate),
		})
	}
	return f, nil
}

func parseAtom(body []byte) (*Feed, error) {
	var doc struct {
		Title    string `xml:"title"`
		Subtitle string `xml:"subtitle"`
		Links    []link `xml:"link"`
		Entries  []struct {
			ID        string `xml:"id"`
			Title     string `xml:"title"`
			Summary   string `xml:"summary"`
			Content   string `xml:"content"`
			Updated   string `xml:"updated"`
			Published string `xml:"published"`
			Author    struct {
				Name string `xml:"name"`
			} `xml:"author"`
			Links []link `xml:"link"`
		} `xml:"entry"`
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = false
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	f := &Feed{Title: html.UnescapeString(doc.Title), Description: doc.Subtitle, SiteURL: atomLink(doc.Links)}
	for _, entry := range doc.Entries {
		content := store.FirstNonEmpty(entry.Content, entry.Summary)
		f.Items = append(f.Items, store.ArticleInput{
			GUID:        store.FirstNonEmpty(entry.ID, atomLink(entry.Links)),
			Title:       html.UnescapeString(entry.Title),
			URL:         atomLink(entry.Links),
			Author:      entry.Author.Name,
			SummaryHTML: entry.Summary,
			ContentHTML: content,
			ImageURL:    store.FirstImage(content),
			PublishedAt: parseTime(store.FirstNonEmpty(entry.Published, entry.Updated)),
		})
	}
	return f, nil
}

type link struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

func atomLink(links []link) string {
	for _, link := range links {
		if link.Rel == "" || link.Rel == "alternate" {
			return store.NormalizeURL(link.Href)
		}
	}
	if len(links) > 0 {
		return store.NormalizeURL(links[0].Href)
	}
	return ""
}

func parseTime(value string) sql.NullTime {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullTime{}
	}
	layouts := []string{time.RFC1123Z, time.RFC1123, time.RFC3339, time.RFC3339Nano, "Mon, 02 Jan 2006 15:04:05 -0700"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return sql.NullTime{Time: t, Valid: true}
		}
	}
	return sql.NullTime{}
}

var iconRelRE = regexp.MustCompile(`(?is)<link[^>]+rel=["'][^"']*(?:icon|apple-touch-icon)[^"']*["'][^>]*>`)
var hrefRE = regexp.MustCompile(`(?is)\shref=["']([^"']+)["']`)

func parseIconLinks(body []byte) []string {
	var links []string
	for _, tag := range iconRelRE.FindAllString(string(body), 10) {
		if match := hrefRE.FindStringSubmatch(tag); len(match) == 2 {
			links = append(links, html.UnescapeString(match[1]))
		}
	}
	return links
}

func resolveURL(baseURL, ref string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return ""
	}
	return base.ResolveReference(u).String()
}

func feedOrigin(feedURL string) string {
	u, err := url.Parse(feedURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
