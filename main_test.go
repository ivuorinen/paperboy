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

// stringCase is one case for a func(string) string. Three of the helpers under
// test have that shape, so they share one table type and one runner rather
// than repeating the scaffolding each time.
type stringCase struct {
	name string
	in   string
	want string
}

// runStringCases runs cases against fn, naming fn in failures so a bare
// subtest name still identifies what broke.
func runStringCases(t *testing.T, fnName string, fn func(string) string, cases []stringCase) {
	t.Helper()

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fn(c.in); got != c.want {
				t.Errorf("%s(%q) = %q, want %q", fnName, c.in, got, c.want)
			}
		})
	}
}

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

// parseFeedItems parses a feed fixture through gofeed, so the item conversion
// is exercised against the real parser without any network access.
func parseFeedItems(t *testing.T, feedXML string) []*gofeed.Item {
	t.Helper()

	feed, err := gofeed.NewParser().Parse(strings.NewReader(feedXML))
	if err != nil {
		t.Fatalf("parsing fixture feed: %v", err)
	}

	return feed.Items
}

// mustTime parses an RFC3339 fixture timestamp or fails the test.
func mustTime(t *testing.T, value string) time.Time {
	t.Helper()

	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("bad fixture timestamp %q: %v", value, err)
	}

	return ts
}

// linesWithPrefix returns the lines of s starting with prefix. Markdown block
// structure is line-anchored, so asserting on these is what distinguishes a
// real heading from the same characters sitting inertly inside a link label.
func linesWithPrefix(s, prefix string) []string {
	var out []string

	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, prefix) {
			out = append(out, line)
		}
	}

	return out
}

// assertOrder fails unless every want appears in haystack, in the given order.
func assertOrder(t *testing.T, haystack string, want ...string) {
	t.Helper()

	prev := -1

	for _, w := range want {
		i := strings.Index(haystack, w)
		if i < 0 {
			t.Fatalf("output missing %q\n---\n%s", w, haystack)
		}
		if i <= prev {
			t.Fatalf("%q appears out of order\n---\n%s", w, haystack)
		}
		prev = i
	}
}

func TestGetURLDomain(t *testing.T) {
	runStringCases(t, "getURLDomain", getURLDomain, []stringCase{
		{"https URL", "https://example.com/a/b", "example.com"},
		{"http URL", "http://example.com", "example.com"},
		{"www stripped", "https://www.example.com/x", "example.com"},
		{"bare www domain", "www.example.com", "example.com"},
		{"bare domain", "example.com", "example.com"},
		{"subdomain kept", "https://news.sub.example.com/x", "news.sub.example.com"},
		{"surrounding space", "  https://example.com  ", "example.com"},
		// Hostnames are case-insensitive; domainRe is not. Before folding,
		// the uppercase-host cases returned "" and the uppercase-www case
		// returned the plausible but wrong "xample.com".
		{"uppercase host", "https://EXAMPLE.com/a", "example.com"},
		{"mixed-case host", "https://Example.COM/a", "example.com"},
		{"uppercase scheme", "HTTPS://EXAMPLE.COM/Some/Path", "example.com"},
		{"uppercase www", "https://WWW.Example.com/a", "example.com"},
		// Regression: url.Parse returns (nil, err) here, and the old code
		// dereferenced the nil *url.URL.
		{"malformed percent escape", "https://example.com/%zz", "example.com"},
		// Regression: the old `^https?` regexp matched this and produced "".
		{"http-prefixed non-URL", "httpfoo.example.com", "httpfoo.example.com"},
		{"empty", "", ""},
		{"no domain present", "not a url", ""},
	})
}

func TestMdText(t *testing.T) {
	runStringCases(t, "mdText", mdText, []stringCase{
		{"plain", "A normal title", "A normal title"},
		{"newlines collapsed", "Line one\n\nLine two", "Line one Line two"},
		{"tabs collapsed", "a\t\tb", "a b"},
		{"brackets escaped", "Review [2026]", `Review \[2026\]`},
		// Escaping only the brackets is bypassable: a trailing backslash
		// turns the escaper's own \] into \\] — a literal backslash plus an
		// unescaped ] — closing the link label early and letting the title
		// supply its own destination.
		{"backslash escaped", `Title\`, `Title\\`},
		{"retarget payload", `x\](https://evil.example)`, `x\\\](https://evil.example)`},
		{"empty", "", ""},
	})
}

// TestGenerateMarkdownRejectsLabelBreakout covers the escape bypass end to end:
// the rendered item must still carry the article's own URL as its destination.
func TestGenerateMarkdownRejectsLabelBreakout(t *testing.T) {
	byWeek := map[string][]Article{
		"2026-01": {{
			Title:     `Free stuff\](https://evil.example/phish)`,
			URL:       "https://real.example/post",
			URLDomain: "real.example",
			PublishAt: mustTime(t, "2026-01-01T00:00:00Z"),
		}},
	}

	got := generateMarkdown("HEAD", "FOOT", byWeek, []string{"2026-01"})

	items := linesWithPrefix(got, "- ")
	if len(items) != 1 {
		t.Fatalf("got %d list-item lines, want 1\n---\n%s", len(items), got)
	}
	if !strings.HasSuffix(items[0], "](https://real.example/post)") {
		t.Errorf("link destination was retargeted by the title: %q", items[0])
	}

	// The precise property: the label must close at the destination we wrote,
	// not at any bracket the title supplied. A substring check cannot express
	// this — the correct output "\\\]" legitimately contains "\\]".
	if got, want := firstUnescapedBracket(items[0]), strings.LastIndex(items[0], "]("); got != want {
		t.Errorf("label closes at byte %d, want %d (the title closed it early): %q", got, want, items[0])
	}
}

// firstUnescapedBracket returns the index of the first "]" that is not itself
// escaped, counting the run of backslashes before it: an even run leaves the
// bracket live, an odd run escapes it. Returns -1 when every bracket is
// escaped.
func firstUnescapedBracket(s string) int {
	for i := range len(s) {
		if s[i] != ']' {
			continue
		}

		backslashes := 0
		for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
			backslashes++
		}

		if backslashes%2 == 0 {
			return i
		}
	}

	return -1
}

func TestMdURL(t *testing.T) {
	runStringCases(t, "mdURL", mdURL, []stringCase{
		{"https passes", "https://example.com/a", "https://example.com/a"},
		{"http passes", "http://example.com/a", "http://example.com/a"},
		{"parens encoded", "https://example.com/a(b)", "https://example.com/a%28b%29"},
		{"space encoded", "https://example.com/a b", "https://example.com/a%20b"},
		{"javascript rejected", "javascript:alert(1)", ""},
		{"data rejected", "data:text/html,<h1>x</h1>", ""},
		{"relative rejected", "/relative/path", ""},
		{"empty rejected", "", ""},
	})
}

// TestItemsToArticlesSkipsUndatedItems is the regression test for the nil
// PublishedParsed dereference. pubDate is optional in RSS 2.0, so this feed is
// valid and previously panicked the whole run.
func TestItemsToArticlesSkipsUndatedItems(t *testing.T) {
	items := parseFeedItems(t, `<?xml version="1.0"?>
<rss version="2.0"><channel>
<title>t</title><link>https://example.com</link><description>d</description>
<item><title>No date</title><link>https://example.com/a</link></item>
<item><title>Dated</title><link>https://example.com/b</link>
<pubDate>Mon, 02 Jan 2006 15:04:05 GMT</pubDate></item>
</channel></rss>`)

	articles := itemsToArticles(items)

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
// <updated>, the common shape for feeds that never set <published>.
func TestItemsToArticlesFallsBackToUpdated(t *testing.T) {
	items := parseFeedItems(t, `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
<title>t</title><id>urn:t</id><updated>2006-01-02T15:04:05Z</updated>
<entry><title>Only updated</title><id>urn:e</id>
<link href="https://example.com/a"/><updated>2006-01-02T15:04:05Z</updated></entry>
</feed>`)

	articles := itemsToArticles(items)

	if len(articles) != 1 {
		t.Fatalf("got %d articles, want 1", len(articles))
	}
	if articles[0].PublishAt.IsZero() {
		t.Error("PublishAt is zero; want the <updated> timestamp")
	}
}

func TestLoadTemplateValid(t *testing.T) {
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
}

func TestLoadTemplateRejectsBadInput(t *testing.T) {
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

func TestLoadConfigValid(t *testing.T) {
	path := writeTemp(t, "config.yaml",
		"feeds:\n  - https://example.com/feed\ntemplate: template.md\noutput: output.md\n")

	config, err := loadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(config.Feeds) != 1 || config.Template != "template.md" || config.Output != "output.md" {
		t.Errorf("parsed config = %+v, want all three fields populated", config)
	}
}

func TestLoadConfigRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		// Regression: a misspelled key previously unmarshalled cleanly and
		// failed only after every feed had been fetched.
		"unknown key": "feeds:\n  - https://e.com/f\ntemplate: template.md\noutout: output.md\n",
		"no feeds":    "template: template.md\noutput: output.md\n",
		"no template": "feeds:\n  - https://e.com/f\noutput: output.md\n",
		"no output":   "feeds:\n  - https://e.com/f\ntemplate: template.md\n",
		"empty file":  "",
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadConfig(writeTemp(t, "config.yaml", content)); err == nil {
				t.Fatalf("want an error for %s, got nil", name)
			}
		})
	}
}

func TestGenerateMarkdownOrdersNewestFirst(t *testing.T) {
	byWeek := map[string][]Article{
		"2026-01": {
			{Title: "Older", URL: "https://a.example/1", URLDomain: "a.example", PublishAt: mustTime(t, "2026-01-01T00:00:00Z")},
			{Title: "Newer", URL: "https://a.example/2", URLDomain: "a.example", PublishAt: mustTime(t, "2026-01-02T00:00:00Z")},
		},
		"2026-02": {
			{Title: "Next week", URL: "https://b.example/1", URLDomain: "b.example", PublishAt: mustTime(t, "2026-01-08T00:00:00Z")},
		},
	}

	got := generateMarkdown("HEAD", "FOOT", byWeek, []string{"2026-02", "2026-01"})

	assertOrder(t, got, "HEAD", "## Week: 2026-02", "Next week", "## Week: 2026-01", "Newer", "Older", "FOOT")
}

// TestGenerateMarkdownNeutralizesInjectingTitle is the regression test for the
// Markdown injection. The injected text is expected to survive as inert
// link-label text: escaping neutralizes structure, it does not censor the
// title. So this asserts on structure, not on substrings.
func TestGenerateMarkdownNeutralizesInjectingTitle(t *testing.T) {
	byWeek := map[string][]Article{
		"2026-01": {{
			Title:     "Benign\n\n## Week: 2099-99\n\n- [Click me](https://evil.example/phish)",
			URL:       "https://a.example/1",
			URLDomain: "a.example",
			PublishAt: mustTime(t, "2026-01-01T00:00:00Z"),
		}},
	}

	got := generateMarkdown("HEAD", "FOOT", byWeek, []string{"2026-01"})

	// An ATX heading needs its # at the start of a line, which is why
	// collapsing the newlines in the title is what defuses the injection.
	if headings := linesWithPrefix(got, "#"); len(headings) != 1 {
		t.Errorf("got %d heading lines, want 1 (the real week heading)\n---\n%s", len(headings), got)
	}

	items := linesWithPrefix(got, "- ")
	if len(items) != 1 {
		t.Fatalf("got %d list-item lines, want 1\n---\n%s", len(items), got)
	}

	// The link destination is whatever follows the final unescaped "](" — it
	// must be the real URL, not the one the title tried to smuggle in.
	if !strings.HasSuffix(items[0], "](https://a.example/1)") {
		t.Errorf("item does not end in the legitimate link target: %q", items[0])
	}
	if !strings.Contains(items[0], `\](https://evil.example/phish)`) {
		t.Errorf("injected target is not sitting behind an escaped bracket: %q", items[0])
	}
}

func TestGenerateMarkdownRejectsUnsafeLink(t *testing.T) {
	byWeek := map[string][]Article{
		"2026-01": {{
			Title:     "Sketchy",
			URL:       "javascript:alert(1)",
			PublishAt: mustTime(t, "2026-01-01T00:00:00Z"),
		}},
	}

	got := generateMarkdown("HEAD", "FOOT", byWeek, []string{"2026-01"})

	if strings.Contains(got, "javascript:") {
		t.Errorf("javascript: target reached the output\n---\n%s", got)
	}
	if !strings.Contains(got, "Sketchy") {
		t.Errorf("title dropped along with the link\n---\n%s", got)
	}
}

func TestGenerateMarkdownNoArticles(t *testing.T) {
	got := generateMarkdown("HEAD", "FOOT", map[string][]Article{}, nil)

	if got != "HEAD\n\nFOOT\n" {
		t.Errorf("got %q, want %q", got, "HEAD\n\nFOOT\n")
	}
}
