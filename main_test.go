// Copyright 2024 Ismo Vuorinen. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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
		// url.Parse keeps IDN hosts in Unicode; an ASCII-only pattern
		// matched from after the last non-ASCII rune, yielding the plausible
		// but wrong "cher.example" and "ller.example".
		{"IDN host", "https://bücher.example/post", "bücher.example"},
		{"IDN host with www", "https://www.müller.example/a", "müller.example"},
		{"underscore label", "https://my_blog.example.com/a", "my_blog.example.com"},
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
		// Raw HTML and autolinks both open with "<" and bind tighter than
		// link brackets, so an unescaped "<" smuggles markup or a live link
		// past the bracket escaping above.
		{"raw html escaped", `<img src=x onerror=alert(1)>`, `\<img src=x onerror=alert(1)>`},
		{"autolink escaped", `<https://evil.example/phish>`, `\<https://evil.example/phish>`},
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

// TestLoadTemplateSplitsOnSeparatorLinesOnly is the regression test for the
// substring split: a "---" anywhere, mid-line or inside a longer horizontal
// rule, used to count as a separator and silently drop header text.
func TestLoadTemplateSplitsOnSeparatorLinesOnly(t *testing.T) {
	cases := []struct {
		name, template, header, footer string
	}{
		{
			"mid-line dashes",
			"# Weekly\n\nIntro --- read on\n\n---\n\n---\n\nFooter\n",
			"# Weekly\n\nIntro --- read on", "Footer",
		},
		{
			"longer horizontal rule",
			"# Weekly\n\n----\n\nIntro paragraph\n\n---\n\n---\n\nFooter\n",
			"# Weekly\n\n----\n\nIntro paragraph", "Footer",
		},
		{
			"CRLF line endings",
			"# Weekly\r\n\r\n---\r\n\r\n---\r\n\r\nFooter\r\n",
			"# Weekly", "Footer",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			header, footer, err := loadTemplate(writeTemp(t, "template.md", c.template))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if header != c.header {
				t.Errorf("header = %q, want %q", header, c.header)
			}
			if footer != c.footer {
				t.Errorf("footer = %q, want %q", footer, c.footer)
			}
		})
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

// TestLoadConfigRejectsUnknownKey is the regression test for silently ignored
// keys: a misspelled key used to unmarshal cleanly and fail only after every
// feed had been fetched. The fixture is otherwise complete, so only the
// unknown-key check can reject it; a fixture missing a required field passes
// even with that check deleted.
func TestLoadConfigRejectsUnknownKey(t *testing.T) {
	path := writeTemp(t, "config.yaml",
		"feeds:\n  - https://e.com/f\ntemplate: template.md\noutput: output.md\nextra: x\n")

	_, err := loadConfig(path)
	if err == nil {
		t.Fatal("want an error for the unknown key, got nil")
	}
	if !strings.Contains(err.Error(), "extra") {
		t.Errorf("error %q does not name the unknown key \"extra\"", err)
	}
}

func TestLoadConfigRejectsBadInput(t *testing.T) {
	cases := map[string]string{
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

// TestGenerateMarkdownOptionalFields covers items without a title or a link,
// both valid in RSS 2.0. An empty title used to render "[](url)", an anchor
// with no visible text, and an empty domain rendered as "@ :".
func TestGenerateMarkdownOptionalFields(t *testing.T) {
	cases := []struct {
		name    string
		article Article
		want    string
	}{
		{
			"untitled",
			Article{URL: "https://a.example/1", URLDomain: "a.example"},
			"- 2026-01-01 @ a.example: [(untitled)](https://a.example/1)",
		},
		{
			"whitespace-only title",
			Article{Title: " \n\t", URL: "https://a.example/1", URLDomain: "a.example"},
			"- 2026-01-01 @ a.example: [(untitled)](https://a.example/1)",
		},
		{
			"no link",
			Article{Title: "No link item"},
			"- 2026-01-01: No link item",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.article.PublishAt = mustTime(t, "2026-01-01T00:00:00Z")
			got := generateMarkdown("HEAD", "FOOT", map[string][]Article{"2026-01": {c.article}}, []string{"2026-01"})

			items := linesWithPrefix(got, "- ")
			if len(items) != 1 {
				t.Fatalf("got %d list-item lines, want 1\n---\n%s", len(items), got)
			}
			if items[0] != c.want {
				t.Errorf("item = %q, want %q", items[0], c.want)
			}
		})
	}
}

// TestGenerateMarkdownTitleCannotRelink covers the plain-text branch: an item
// whose link is rejected must stay unlinked, so its title must not be able to
// reintroduce a link as a CommonMark autolink.
func TestGenerateMarkdownTitleCannotRelink(t *testing.T) {
	byWeek := map[string][]Article{
		"2026-01": {{
			Title:     "<https://evil.example/phish>",
			URL:       "javascript:alert(1)",
			URLDomain: "a.example",
			PublishAt: mustTime(t, "2026-01-01T00:00:00Z"),
		}},
	}

	got := generateMarkdown("HEAD", "FOOT", byWeek, []string{"2026-01"})

	items := linesWithPrefix(got, "- ")
	if len(items) != 1 {
		t.Fatalf("got %d list-item lines, want 1\n---\n%s", len(items), got)
	}
	if want := "- 2026-01-01 @ a.example: \\<https://evil.example/phish>"; items[0] != want {
		t.Errorf("item = %q, want %q", items[0], want)
	}
}

// writeRunConfig writes a valid template and a config naming feeds, the
// template and output, returning the config path. Paths are absolute, so run
// does not depend on the test's working directory.
func writeRunConfig(t *testing.T, output string, feeds ...string) string {
	t.Helper()

	template := writeTemp(t, "template.md", "# Head\n\n---\n\n---\n\nFoot\n")

	var b strings.Builder
	b.WriteString("feeds:\n")
	for _, feed := range feeds {
		fmt.Fprintf(&b, "  - %q\n", feed)
	}
	fmt.Fprintf(&b, "template: %q\noutput: %q\n", template, output)

	return writeTemp(t, "config.yaml", b.String())
}

// TestRunAllFeedsFailedKeepsOutput is the regression test for the fail-open
// run: with every feed down it used to overwrite the last digest with an empty
// one and exit 0, so a cron job mailed an empty newsletter.
func TestRunAllFeedsFailedKeepsOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	output := writeTemp(t, "output.md", "previous digest\n")

	if err := run(writeRunConfig(t, output, srv.URL+"/a", srv.URL+"/b")); err == nil {
		t.Fatal("run returned nil with every feed failing, want an error")
	}

	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if string(got) != "previous digest\n" {
		t.Errorf("output = %q, want the previous digest left untouched", got)
	}
}

// rssFeed wraps items in a minimal RSS 2.0 document.
func rssFeed(items ...string) string {
	return `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>` +
		`<link>https://example.com</link><description>d</description>` +
		strings.Join(items, "") + `</channel></rss>`
}

// rssItem renders one dated item linking to https://example.com/<slug>.
func rssItem(title, slug, pubDate string) string {
	return fmt.Sprintf("<item><title>%s</title><link>https://example.com/%s</link><pubDate>%s</pubDate></item>",
		title, slug, pubDate)
}

// feedServer serves each path in feeds as an RSS body and answers every other
// path with HTTP 500, standing in for an unreachable feed.
func feedServer(t *testing.T, feeds map[string]string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := feeds[r.URL.Path]
		if !ok {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv
}

// runToString runs one cycle over feeds and returns the written output.
func runToString(t *testing.T, feeds ...string) string {
	t.Helper()

	output := writeTemp(t, "output.md", "previous digest\n")
	if err := run(writeRunConfig(t, output, feeds...)); err != nil {
		t.Fatalf("run: %v", err)
	}

	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}

	return string(got)
}

// TestRunGroupsByISOWeek drives the whole pipeline with one healthy and one
// failing feed. Weeks are ISO weeks: 2024-12-30 belongs to 2025-01 and
// 2027-01-01 to 2026-53 (checked with `date +%G-%V`), which a calendar-year
// grouping would get wrong at both ends of the year.
func TestRunGroupsByISOWeek(t *testing.T) {
	srv := feedServer(t, map[string]string{
		"/ok": rssFeed(
			rssItem("Boundary", "1", "Mon, 30 Dec 2024 12:00:00 GMT"),
			rssItem("Year end", "2", "Fri, 01 Jan 2027 12:00:00 GMT"),
		),
	})

	got := runToString(t, srv.URL+"/ok", srv.URL+"/down")

	assertOrder(t, got, "# Head", "## Week: 2026-53", "Year end", "## Week: 2025-01", "Boundary", "Foot")
	if strings.Contains(got, "previous digest") {
		t.Errorf("a partial failure must still replace the old digest\n---\n%s", got)
	}
}

// TestRunSendsUserAgent checks the identifying User-Agent reaches the feed
// server; gofeed's default is "Gofeed/1.0", which some hosts block.
func TestRunSendsUserAgent(t *testing.T) {
	var agent string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(rssFeed(rssItem("Post", "1", "Tue, 06 Jan 2026 12:00:00 GMT"))))
	}))
	t.Cleanup(srv.Close)

	runToString(t, srv.URL)

	if !strings.HasPrefix(agent, "paperboy/") {
		t.Errorf("User-Agent = %q, want it to start with %q", agent, "paperboy/")
	}
}

// TestRunCapsFeedSize serves a valid feed one byte over maxFeedBytes next to a
// small one. Without the cap gofeed reads the whole body and the oversized
// feed's article reaches the digest.
func TestRunCapsFeedSize(t *testing.T) {
	huge := rssFeed(
		"<item><title>Huge</title><link>https://example.com/h</link><pubDate>Tue, 06 Jan 2026 12:00:00 GMT</pubDate>" +
			"<description>" + strings.Repeat("a", maxFeedBytes+1) + "</description></item>",
	)
	srv := feedServer(t, map[string]string{
		"/huge":  huge,
		"/small": rssFeed(rssItem("Small", "s", "Tue, 06 Jan 2026 12:00:00 GMT")),
	})

	got := runToString(t, srv.URL+"/huge", srv.URL+"/small")

	if !strings.Contains(got, "Small") {
		t.Errorf("the small feed's article is missing\n---\n%s", got)
	}
	if strings.Contains(got, "Huge") {
		t.Error("the oversized feed was read past maxFeedBytes and rendered")
	}
}

// TestWriteFileAtomicReplacesWholeFile pins the rename-over semantics. A reader
// holding the old file keeps the old bytes only if the path was swapped to a
// new file; os.WriteFile truncates the same inode in place, which is what let a
// failed write destroy the previous digest.
//
// The replacement also keeps the old file's mode, as os.WriteFile did by never
// recreating it. 0640 differs from both CreateTemp's 0600 and a forced 0644,
// so neither leaving the temp file's mode nor overriding it passes.
func TestWriteFileAtomicReplacesWholeFile(t *testing.T) {
	path := writeTemp(t, "output.md", "previous digest\n")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatalf("setting output mode: %v", err)
	}

	old, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening old output: %v", err)
	}
	t.Cleanup(func() { _ = old.Close() })

	if err := writeFileAtomic(path, []byte("new digest\n")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	stale := make([]byte, 64)
	n, _ := old.Read(stale)
	if got := string(stale[:n]); got != "previous digest\n" {
		t.Errorf("reader of the old file saw %q, want the old bytes intact", got)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading new output: %v", err)
	}
	if string(got) != "new digest\n" {
		t.Errorf("output = %q, want %q", got, "new digest\n")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o640 {
		t.Errorf("output mode = %o, want the existing file's 640 kept", mode)
	}

	assertNoTempFiles(t, filepath.Dir(path))
}

// TestWriteFileAtomicCleansUpOnFailure renames onto a directory, which fails
// after the temp file exists; the temp file must not be left behind.
func TestWriteFileAtomicCleansUpOnFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "output.md")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("creating blocking directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), nil, 0o600); err != nil {
		t.Fatalf("populating blocking directory: %v", err)
	}

	if err := writeFileAtomic(target, []byte("x")); err == nil {
		t.Fatal("writeFileAtomic onto a non-empty directory returned nil, want an error")
	}

	assertNoTempFiles(t, dir)
}

// TestWriteFileAtomicFollowsSymlink keeps a symlinked output working: os.WriteFile
// wrote through the link, and a bare rename onto the link path would replace
// the link itself with a regular file, so the real target stops updating.
//
// The dangling case is a link set up before the first run, pointing at a
// digest that does not exist yet: os.WriteFile created the target through it.
func TestWriteFileAtomicFollowsSymlink(t *testing.T) {
	cases := []struct {
		name   string
		target func(t *testing.T) string
	}{
		{"existing target", func(t *testing.T) string {
			return writeTemp(t, "served.md", "previous digest\n")
		}},
		{"dangling link", func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "served.md")
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target := c.target(t)
			link := filepath.Join(t.TempDir(), "output.md")
			if err := os.Symlink(target, link); err != nil {
				t.Fatalf("creating symlink: %v", err)
			}

			if err := writeFileAtomic(link, []byte("new digest\n")); err != nil {
				t.Fatalf("writeFileAtomic: %v", err)
			}

			info, err := os.Lstat(link)
			if err != nil {
				t.Fatalf("lstat link: %v", err)
			}
			if info.Mode()&os.ModeSymlink == 0 {
				t.Errorf("output path is now a %v, want it to stay a symlink", info.Mode().Type())
			}

			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("reading target: %v", err)
			}
			if string(got) != "new digest\n" {
				t.Errorf("symlink target = %q, want %q", got, "new digest\n")
			}
		})
	}
}

// assertNoTempFiles fails if writeFileAtomic left a temp file in dir.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()

	leftovers, err := filepath.Glob(filepath.Join(dir, ".paperboy-*"))
	if err != nil {
		t.Fatalf("globbing temp files: %v", err)
	}
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestGenerateMarkdownNoArticles(t *testing.T) {
	got := generateMarkdown("HEAD", "FOOT", map[string][]Article{}, nil)

	if got != "HEAD\n\nFOOT\n" {
		t.Errorf("got %q, want %q", got, "HEAD\n\nFOOT\n")
	}
}
