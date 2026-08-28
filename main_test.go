// Copyright 2024 Ismo Vuorinen. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

// writeTemp writes content to a file in the test's temp dir and returns its
// path, so template and config cases do not depend on the working directory.
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}

	return path
}

func TestGetURLDomain(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"https URL", "https://example.com/a/b", "example.com"},
		{"http URL", "http://example.com", "example.com"},
		{"www stripped", "https://www.example.com/x", "example.com"},
		{"bare www domain", "www.example.com", "example.com"},
		{"bare domain", "example.com", "example.com"},
		{"subdomain kept", "https://news.sub.example.com/x", "news.sub.example.com"},
		{"surrounding space", "  https://example.com  ", "example.com"},
		// Regression: url.Parse returns (nil, err) here, and the old code
		// dereferenced the nil *url.URL.
		{"malformed percent escape", "https://example.com/%zz", "example.com"},
		// Regression: the old `^https?` regexp matched this and produced "".
		{"http-prefixed non-URL", "httpfoo.example.com", "httpfoo.example.com"},
		{"empty", "", ""},
		{"no domain present", "not a url", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getURLDomain(tt.in); got != tt.want {
				t.Errorf("getURLDomain(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestItemsToArticlesSkipsUndatedItems is the regression test for the nil
// PublishedParsed dereference. pubDate is optional in RSS 2.0, so this feed is
// valid and previously panicked the whole run.
func TestItemsToArticlesSkipsUndatedItems(t *testing.T) {
	const feedXML = `<?xml version="1.0"?>
<rss version="2.0"><channel>
<title>t</title><link>https://example.com</link><description>d</description>
<item><title>No date</title><link>https://example.com/a</link></item>
<item><title>Dated</title><link>https://example.com/b</link>
<pubDate>Mon, 02 Jan 2006 15:04:05 GMT</pubDate></item>
</channel></rss>`

	feed, err := gofeed.NewParser().Parse(strings.NewReader(feedXML))
	if err != nil {
		t.Fatalf("parsing fixture feed: %v", err)
	}

	articles := itemsToArticles(feed.Items)

	if len(articles) != 1 {
		t.Fatalf("got %d articles, want 1 (the undated item must be dropped)", len(articles))
	}
	if articles[0].Title != "Dated" {
		t.Errorf("kept %q, want the dated item", articles[0].Title)
	}
	if articles[0].URLDomain != "example.com" {
		t.Errorf("URLDomain = %q, want example.com", articles[0].URLDomain)
	}
}

// TestItemsToArticlesFallsBackToUpdated covers an Atom entry with only
// <updated>, which is the common shape for feeds that never set <published>.
func TestItemsToArticlesFallsBackToUpdated(t *testing.T) {
	const feedXML = `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
<title>t</title><id>urn:t</id><updated>2006-01-02T15:04:05Z</updated>
<entry><title>Only updated</title><id>urn:e</id>
<link href="https://example.com/a"/><updated>2006-01-02T15:04:05Z</updated></entry>
</feed>`

	feed, err := gofeed.NewParser().Parse(strings.NewReader(feedXML))
	if err != nil {
		t.Fatalf("parsing fixture feed: %v", err)
	}

	articles := itemsToArticles(feed.Items)

	if len(articles) != 1 {
		t.Fatalf("got %d articles, want 1", len(articles))
	}
	if articles[0].PublishAt.IsZero() {
		t.Error("PublishAt is zero; want the <updated> timestamp")
	}
}

func TestMdText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "A normal title", "A normal title"},
		{"newlines collapsed", "Line one\n\nLine two", "Line one Line two"},
		{"tabs collapsed", "a\t\tb", "a b"},
		{"brackets escaped", "Review [2026]", `Review \[2026\]`},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mdText(tt.in); got != tt.want {
				t.Errorf("mdText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMdURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"https passes", "https://example.com/a", "https://example.com/a"},
		{"http passes", "http://example.com/a", "http://example.com/a"},
		{"parens encoded", "https://example.com/a(b)", "https://example.com/a%28b%29"},
		{"space encoded", "https://example.com/a b", "https://example.com/a%20b"},
		{"javascript rejected", "javascript:alert(1)", ""},
		{"data rejected", "data:text/html,<h1>x</h1>", ""},
		{"relative rejected", "/relative/path", ""},
		{"empty rejected", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mdURL(tt.in); got != tt.want {
				t.Errorf("mdURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLoadTemplate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		path := writeTemp(t, "template.md", "# Header\n\n---\n\n---\n\nFooter text\n")

		header, footer, err := loadTemplate(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if header != "# Header" {
			t.Errorf("header = %q, want %q", header, "# Header")
		}
		if footer != "Footer text" {
			t.Errorf("footer = %q, want %q", footer, "Footer text")
		}
	})

	t.Run("too few separators", func(t *testing.T) {
		path := writeTemp(t, "template.md", "# Header\n\n---\n\nFooter\n")

		if _, _, err := loadTemplate(path); err == nil {
			t.Fatal("want an error for a template with one --- separator, got nil")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		if _, _, err := loadTemplate(filepath.Join(t.TempDir(), "absent.md")); err == nil {
			t.Fatal("want an error for a missing template, got nil")
		}
	})
}

func TestLoadConfig(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		path := writeTemp(t, "config.yaml",
			"feeds:\n  - https://example.com/feed\ntemplate: template.md\noutput: output.md\n")

		config, err := loadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(config.Feeds) != 1 || config.Template != "template.md" || config.Output != "output.md" {
			t.Errorf("parsed config = %+v, want all three fields populated", config)
		}
	})

	// Regression: a misspelled key previously unmarshalled cleanly and failed
	// only after every feed had been fetched.
	t.Run("unknown key rejected", func(t *testing.T) {
		path := writeTemp(t, "config.yaml",
			"feeds:\n  - https://example.com/feed\ntemplate: template.md\noutout: output.md\n")

		if _, err := loadConfig(path); err == nil {
			t.Fatal("want an error for the misspelled 'outout' key, got nil")
		}
	})

	t.Run("missing required fields", func(t *testing.T) {
		cases := map[string]string{
			"no feeds":    "template: template.md\noutput: output.md\n",
			"no template": "feeds:\n  - https://example.com/feed\noutput: output.md\n",
			"no output":   "feeds:\n  - https://example.com/feed\ntemplate: template.md\n",
			"empty file":  "",
		}

		for name, content := range cases {
			t.Run(name, func(t *testing.T) {
				if _, err := loadConfig(writeTemp(t, "config.yaml", content)); err == nil {
					t.Fatalf("want an error for %s, got nil", name)
				}
			})
		}
	})
}

func TestGenerateMarkdown(t *testing.T) {
	at := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("bad fixture timestamp %q: %v", s, err)
		}
		return ts
	}

	t.Run("orders weeks and articles newest first", func(t *testing.T) {
		byWeek := map[string][]Article{
			"2026-01": {
				{Title: "Older", URL: "https://a.example/1", URLDomain: "a.example", PublishAt: at("2026-01-01T00:00:00Z")},
				{Title: "Newer", URL: "https://a.example/2", URLDomain: "a.example", PublishAt: at("2026-01-02T00:00:00Z")},
			},
			"2026-02": {
				{Title: "Next week", URL: "https://b.example/1", URLDomain: "b.example", PublishAt: at("2026-01-08T00:00:00Z")},
			},
		}
		weeks := []string{"2026-02", "2026-01"}

		got := generateMarkdown("HEAD", "FOOT", byWeek, weeks)

		wantOrder := []string{"HEAD", "## Week: 2026-02", "Next week", "## Week: 2026-01", "Newer", "Older", "FOOT"}
		prev := -1
		for _, want := range wantOrder {
			i := strings.Index(got, want)
			if i < 0 {
				t.Fatalf("output missing %q\n---\n%s", want, got)
			}
			if i <= prev {
				t.Fatalf("%q appears out of order\n---\n%s", want, got)
			}
			prev = i
		}
	})

	// Regression: an unescaped title could end its list item and forge a week
	// heading and an arbitrary link into the document.
	t.Run("neutralizes an injecting title", func(t *testing.T) {
		byWeek := map[string][]Article{
			"2026-01": {{
				Title:     "Benign\n\n## Week: 2099-99\n\n- [Click me](https://evil.example/phish)",
				URL:       "https://a.example/1",
				URLDomain: "a.example",
				PublishAt: at("2026-01-01T00:00:00Z"),
			}},
		}

		got := generateMarkdown("HEAD", "FOOT", byWeek, []string{"2026-01"})

		// The injected text is expected to survive as inert link-label text;
		// escaping neutralizes structure, it does not censor the title. So
		// these assertions check structure, not substrings.
		var headings, items int
		for _, line := range strings.Split(got, "\n") {
			// An ATX heading needs its # at the start of a line, which is why
			// mdText collapsing newlines is what defuses the injection.
			if strings.HasPrefix(line, "#") {
				headings++
			}
			if strings.HasPrefix(line, "- ") {
				items++
				// The link destination is whatever follows the final
				// unescaped "](" — it must be the real URL, not the one the
				// title tried to smuggle in.
				if !strings.HasSuffix(line, "](https://a.example/1)") {
					t.Errorf("list item does not end in the legitimate link target: %q", line)
				}
				if strings.Contains(line, "]("+"https://evil.example/phish)") &&
					!strings.Contains(line, `\](https://evil.example/phish)`) {
					t.Errorf("injected target sits behind an unescaped bracket: %q", line)
				}
			}
		}

		if headings != 1 {
			t.Errorf("got %d heading lines, want 1 (the real week heading)\n---\n%s", headings, got)
		}
		if items != 1 {
			t.Errorf("got %d list-item lines, want 1\n---\n%s", items, got)
		}
	})

	t.Run("renders a rejected link as plain text", func(t *testing.T) {
		byWeek := map[string][]Article{
			"2026-01": {{
				Title:     "Sketchy",
				URL:       "javascript:alert(1)",
				URLDomain: "",
				PublishAt: at("2026-01-01T00:00:00Z"),
			}},
		}

		got := generateMarkdown("HEAD", "FOOT", byWeek, []string{"2026-01"})

		if strings.Contains(got, "javascript:") {
			t.Errorf("javascript: target reached the output\n---\n%s", got)
		}
		if !strings.Contains(got, "Sketchy") {
			t.Errorf("title dropped along with the link\n---\n%s", got)
		}
	})

	t.Run("no articles", func(t *testing.T) {
		got := generateMarkdown("HEAD", "FOOT", map[string][]Article{}, nil)

		if got != "HEAD\n\nFOOT\n" {
			t.Errorf("got %q, want %q", got, "HEAD\n\nFOOT\n")
		}
	})
}
