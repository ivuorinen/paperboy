// Copyright 2024 Ismo Vuorinen. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.
// SPDX-License-Identifier: MIT
//
// Paperboy is a simple RSS feed reader that generates
// a Markdown file with the latest articles from multiple feeds.

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"go.yaml.in/yaml/v3"
)

// version and build are stamped by the linker; see the build target in the
// Makefile. Both must stay constant-initialized or -X is silently ignored,
// which is why build is not time.Now(): that reported the run date as the
// build date on every invocation.
var (
	version = "dev"
	build   = "unknown"
)

// maxFeedBytes bounds how much of a remote feed response is read into memory.
// gofeed defaults to no limit, so a hostile or misconfigured server could
// otherwise exhaust memory; ParseURL's own 30s timeout does not bound a fast
// server streaming a large body.
const maxFeedBytes = 16 << 20

// domainRe matches the trailing dotted-label sequence of a host. Compiled once
// at package scope: MustCompile panics on a bad pattern, which belongs at
// startup rather than part-way through a run.
//
// Labels are Unicode letters, digits, "_" and "-": url.Parse keeps IDN hosts in
// Unicode, and an ASCII-only class matched from after the last non-ASCII rune,
// turning "bücher.example" into the plausible but wrong "cher.example".
var domainRe = regexp.MustCompile(`([\p{L}\p{N}_-]+\.)+[\p{L}\p{N}_-]+`)

// templateSep matches a template separator: a line holding only "---", with
// optional trailing blanks and a CRLF ending. Splitting on the bare substring
// also cut at "---" mid-line and inside longer horizontal rules such as
// "----", silently dropping the header text in between.
var templateSep = regexp.MustCompile(`(?m)^---[ \t]*\r?$`)

// mdTextEscaper escapes the brackets that would close a Markdown link label
// early, letting remote text break out of the label it is placed in.
//
// The backslash entry must stay first and must not be dropped: escaping only
// the brackets is bypassable. A title ending in a backslash before a bracket
// emits \\] — a literal backslash followed by an UNESCAPED ] — which closes
// the label early and hands the following (...) to the reader as the link
// destination. Verified with CommonMark: the title
// `Free stuff\](https://evil.example/phish)` rendered an anchor pointing at
// evil.example rather than at the article. strings.NewReplacer does not
// rescan its own output, so escaping the escape character in the same
// Replacer is correct and does not double-apply.
//
// "<" is escaped because CommonMark raw HTML and autolinks both open with it
// and bind tighter than link brackets, so bracket escaping alone does not stop
// them. Unescaped, a feed title `<https://evil.example/phish>` rendered as a
// live link even under goldmark's safe defaults, and `<img onerror=...>` or
// `<h1>` passed through any renderer that allows raw HTML. A backslash-escaped
// "<" is literal text, which disables both constructs.
var mdTextEscaper = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`, "<", `\<`)

// mdURLEscaper percent-encodes the characters that would terminate a Markdown
// link target early. net/url leaves parentheses unescaped in paths, since they
// are legal sub-delims, so url.String alone is not sufficient here.
var mdURLEscaper = strings.NewReplacer(
	" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E",
)

// Config represents the structure of the YAML configuration file
type Config struct {
	Template string   `yaml:"template"`
	Output   string   `yaml:"output"`
	Feeds    []string `yaml:"feeds"`
}

// Article represents a feed article
type Article struct {
	PublishAt time.Time
	Title     string
	URL       string
	URLDomain string
}

func main() {
	log.Printf("Paperboy v.%s (build %s)", version, build)

	configFile := flag.String("config", "config.yaml", "path to the YAML configuration file")
	flag.Parse()

	if err := run(*configFile); err != nil {
		log.Fatalf("Error %v", err)
	}
}

// run performs one fetch-and-write cycle for the configuration at configFile.
// It is main without the process exit, so the whole pipeline — config,
// template, fetching, week grouping and the output write — runs under test.
func run(configFile string) error {
	config, err := loadConfig(configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Read the template before any network work: a malformed template then
	// costs a second rather than a full fetch cycle over every feed.
	header, footer, err := loadTemplate(config.Template)
	if err != nil {
		return fmt.Errorf("loading template: %w", err)
	}

	log.Printf("Feeds: %d", len(config.Feeds))

	fp := gofeed.NewParser()
	fp.MaxByteSize = maxFeedBytes
	fp.UserAgent = fmt.Sprintf("paperboy/%s (+https://github.com/ivuorinen/paperboy)", version)

	articlesByWeek := make(map[string][]Article)
	fetched := 0

	for _, feedURL := range config.Feeds {
		log.Printf("Fetching articles from %s", feedURL)

		articles, err := fetchArticles(fp, feedURL)
		if err != nil {
			log.Printf("Error fetching articles from %s: %v", feedURL, err)
			continue
		}

		fetched++
		log.Printf("-> Got %d articles", len(articles))

		// Group articles by publish week
		for _, article := range articles {
			year, week := article.PublishAt.UTC().ISOWeek()
			// Format week in the format "YYYY-WW"
			// e.g. 2021-01
			id := fmt.Sprintf("%d-%02d", year, week)
			articlesByWeek[id] = append(articlesByWeek[id], article)
		}
	}

	// A single failing feed is logged and skipped, but losing every feed is an
	// outage (network, DNS, proxy), not news: writing would replace the last
	// good digest with an empty one and report success to cron.
	if fetched == 0 {
		return fmt.Errorf("fetching feeds: all %d failed; %s left untouched", len(config.Feeds), config.Output)
	}

	// Newest week first. Derived from the map rather than tracked alongside it,
	// so the two cannot disagree.
	weeks := slices.Sorted(maps.Keys(articlesByWeek))
	slices.Reverse(weeks)

	log.Printf("-> Sorted and reversed %d weeks", len(weeks))

	output := generateMarkdown(header, footer, articlesByWeek, weeks)

	log.Printf("-> Generated Markdown output")

	if err := writeFileAtomic(config.Output, []byte(output)); err != nil {
		return fmt.Errorf("writing output file: %w", err)
	}

	log.Printf("-> Wrote output to %s", config.Output)
	log.Printf("Paperboy finished")

	return nil
}

// writeFileAtomic replaces path with data so a reader sees either the old file
// or the new one, never a truncated mix. os.WriteFile truncates in place, so a
// failure after the truncate (ENOSPC, a kill mid-write) destroyed the previous
// digest. The temp file is created beside path because rename is atomic only
// within one filesystem, and it is removed on any failure.
//
// The replacement takes the current file's mode, which os.WriteFile kept by
// never recreating the file. CreateTemp makes 0600, and forcing any fixed mode
// instead would widen a private digest or override the umask. A first run
// gets os.WriteFile's 0644 narrowed by the umask, from the O_CREATE open below.
//
// A symlinked path is resolved first, so the link's target is replaced as
// os.WriteFile would have written through it; renaming onto the link path
// would swap the link itself for a regular file.
//
// A missing path or an existing regular file is first opened for writing,
// with O_CREATE and no O_TRUNC, the open os.WriteFile made. The kernel then
// resolves a link as before and creates a missing target, including the
// not-yet-written target of a link set up before the first run; without it,
// EvalSymlinks fails on a dangling link and the rename replaces the link. The
// same open makes a read-only output fail as it did under os.WriteFile,
// where a rename alone needs only the directory's write permission. A
// directory skips the open and fails at the rename. Writing therefore needs
// write access to the file, as os.WriteFile did, and to its directory.
func writeFileAtomic(path string, data []byte) (err error) {
	if info, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) || (statErr == nil && info.Mode().IsRegular()) {
		var f *os.File
		if f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o644); err != nil {
			return err
		}

		if err = f.Close(); err != nil {
			return err
		}
	}

	if path, err = filepath.EvalSymlinks(path); err != nil {
		return err
	}

	current, err := os.Stat(path)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".paperboy-*")
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}

	if err = tmp.Close(); err != nil {
		return err
	}

	if err = os.Chmod(tmp.Name(), current.Mode().Perm()); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), path)
}

// loadConfig reads and validates the YAML configuration.
//
// Unknown keys are an error rather than a silent omission: a misspelled
// "output" key would otherwise leave the field empty and fail only after every
// feed had been fetched, with an error naming an empty path. An empty file is
// allowed through the decoder and caught by the required-field checks below,
// which say what is missing instead of reporting "EOF".
func loadConfig(path string) (Config, error) {
	var config Config

	data, err := os.ReadFile(path)
	if err != nil {
		return config, fmt.Errorf("reading %s: %w", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	if err := dec.Decode(&config); err != nil && !errors.Is(err, io.EOF) {
		return config, fmt.Errorf("parsing %s: %w", path, err)
	}

	switch {
	case len(config.Feeds) == 0:
		return config, fmt.Errorf("%s: no feeds configured", path)
	case config.Template == "":
		return config, fmt.Errorf("%s: template is required", path)
	case config.Output == "":
		return config, fmt.Errorf("%s: output is required", path)
	}

	return config, nil
}

// loadTemplate reads the template and splits it into the header and footer
// that surround the generated article list, separated by the two "---" lines
// the README documents. Returns an error rather than terminating: it is called
// before any feed is fetched precisely so the caller can fail cheaply.
func loadTemplate(path string) (header, footer string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("reading template %s: %w", path, err)
	}

	parts := templateSep.Split(string(data), 3)
	if len(parts) != 3 {
		return "", "", fmt.Errorf(
			"template %s: want a header and footer separated by two --- lines, found %d section(s)",
			path, len(parts),
		)
	}

	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[2]), nil
}

// fetchArticles fetches articles from a given feed URL
func fetchArticles(fp *gofeed.Parser, feedURL string) ([]Article, error) {
	feed, err := fp.ParseURL(feedURL)
	if err != nil {
		// %w, not %v: callers cannot otherwise distinguish
		// gofeed.ErrResponseTooLarge or a context deadline from an HTTP 404
		// without matching on strings.
		return nil, fmt.Errorf("parsing feed: %w", err)
	}

	return itemsToArticles(feed.Items), nil
}

// itemsToArticles converts feed items, dropping any it cannot date.
//
// PublishedParsed is nil whenever a feed omits a publication date — optional in
// both RSS 2.0 and Atom — or supplies one gofeed cannot parse. Dereferencing it
// panicked the whole run, and a panic escapes the per-feed error handling in
// main, so a single dateless item discarded every feed already fetched.
//
// Split out of fetchArticles so the conversion is testable without network I/O.
func itemsToArticles(items []*gofeed.Item) []Article {
	var articles []Article

	for _, item := range items {
		published := item.PublishedParsed
		if published == nil {
			published = item.UpdatedParsed
		}
		if published == nil {
			log.Printf("-> skipping %q: no parseable publish date", item.Title)
			continue
		}

		articles = append(articles, Article{
			Title:     item.Title,
			URL:       item.Link,
			PublishAt: published.UTC(),
			URLDomain: getURLDomain(item.Link),
		})
	}

	return articles
}

// generateMarkdown renders the article list between the template's header and
// footer.
//
// Titles and links originate in remote feeds and are escaped at this boundary:
// an unescaped title containing newlines could otherwise end its list item and
// forge week headings and links into the output document, which the README
// suggests mailing out.
func generateMarkdown(header, footer string, articlesByWeek map[string][]Article, weeks []string) string {
	var output strings.Builder

	output.WriteString(header)
	output.WriteString("\n\n")

	for _, week := range weeks {
		articles := articlesByWeek[week]

		// Newest first, matching the week ordering above.
		slices.SortFunc(articles, func(a, b Article) int {
			return b.PublishAt.Compare(a.PublishAt)
		})

		fmt.Fprintf(&output, "## Week: %s\n\n", week)

		for _, article := range articles {
			// The "@ domain" part is omitted for an item with no link to take
			// a domain from; otherwise the line read "@ :".
			prefix := article.PublishAt.Format("2006-01-02")
			if article.URLDomain != "" {
				prefix += " @ " + article.URLDomain
			}

			// RSS 2.0 allows items without a title, and an empty label renders
			// as an anchor with no visible text.
			title := mdText(article.Title)
			if title == "" {
				title = "(untitled)"
			}

			// A link that is not a plain absolute http(s) URL is dropped
			// rather than rendered, leaving the entry as plain text.
			if link := mdURL(article.URL); link != "" {
				fmt.Fprintf(&output, "- %s: [%s](%s)\n", prefix, title, link)
				continue
			}

			fmt.Fprintf(&output, "- %s: %s\n", prefix, title)
		}

		output.WriteString("\n")
	}

	output.WriteString(footer)
	output.WriteString("\n")

	return output.String()
}

// mdText makes remote text safe inside a Markdown link label: whitespace runs,
// newlines included, collapse to single spaces so the text cannot end the list
// item, and the bracket characters that would close the label are escaped.
func mdText(s string) string {
	return mdTextEscaper.Replace(strings.Join(strings.Fields(s), " "))
}

// mdURL returns raw as a Markdown-safe absolute http(s) URL, or "" when it is
// neither. Restricting the scheme keeps a feed from emitting javascript: or
// data: link targets.
func mdURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}

	return mdURLEscaper.Replace(parsed.String())
}

// getURLDomain extracts the domain from a URL-like string
// e.g. "https://example.com" -> "example.com"
//
// A parse failure falls through to the raw string rather than propagating:
// url.Parse returns a nil *url.URL alongside its error, and dereferencing that
// panicked on links carrying a malformed percent-escape. The scheme test is a
// prefix check rather than the regexp `^https?`, which also matched "httpfoo"
// and yielded an empty host for it.
//
// Case folding happens first because domainRe matches lowercase only while
// url.Parse preserves host case. Without it "https://WWW.Example.com" misses
// the www. trim and then matches from the first lowercase run, returning the
// plausible but wrong "xample.com". Folding early also lets the scheme check
// below catch "HTTPS://".
func getURLDomain(urlString string) string {
	urlString = strings.ToLower(strings.TrimSpace(urlString))

	if strings.HasPrefix(urlString, "http://") || strings.HasPrefix(urlString, "https://") {
		if parsed, err := url.Parse(urlString); err == nil {
			urlString = parsed.Host
		}
	}

	urlString = strings.TrimPrefix(urlString, "www.")

	return domainRe.FindString(urlString)
}
