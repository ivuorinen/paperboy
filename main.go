// Copyright 2024 Ismo Vuorinen. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.
// SPDX-License-Identifier: MIT
//
// Paperboy is a simple RSS feed reader that generates
// a Markdown file with the latest articles from multiple feeds.

//go:build go1.22
// +build go1.22

package main

import (
	"cmp"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"gopkg.in/yaml.v3"
)

// Version and Build information
// These variables are set during build time
var (
	version string = "dev"
	build   string = time.Now().Format("20060102")
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

	// Read YAML configuration file
	configFile := "config.yaml"
	configData, err := os.ReadFile(configFile)
	if err != nil {
		log.Fatalf("Error reading config.yaml file: %v", err)
	}

	// Parse YAML configuration
	var config Config
	err = yaml.Unmarshal(configData, &config)
	if err != nil {
		log.Fatalf("Error parsing config.yaml file: %v", err)
	}

	log.Printf("Feeds: %d", len(config.Feeds))

	// Fetch articles from each feed URL
	articlesByWeek := make(map[string][]Article)
	var weeks []string

	for _, feedURL := range config.Feeds {

		log.Printf("Fetching articles from %s", feedURL)

		articles, err := fetchArticles(feedURL)
		if err != nil {
			log.Printf("Error fetching articles from %s: %v", feedURL, err)
			continue
		}

		log.Printf("-> Got %d articles", len(articles))

		// Group articles by publish week
		for _, article := range articles {
			year, week := article.PublishAt.UTC().ISOWeek()
			// Format week in the format "YYYY-WW"
			// e.g. 2021-01
			id := fmt.Sprintf("%d-%02d", year, week)
			articlesByWeek[id] = append(articlesByWeek[id], article)

			if !slices.Contains(weeks, id) {
				weeks = append(weeks, id)
			}
		}
	}

	// Sort weeks
	sort.Strings(weeks)
	slices.Reverse(weeks)

	log.Printf("-> Sorted and reversed %d weeks", len(weeks))

	// Generate Markdown output
	output := generateMarkdown(config.Template, articlesByWeek, weeks)

	log.Printf("-> Generated Markdown output")

	// Write Markdown output to file
	outputFile := config.Output
	err = os.WriteFile(outputFile, []byte(output), 0644)
	if err != nil {
		log.Fatalf("Error writing output file: %v", err)
	}

	log.Printf("-> Wrote output to %s", outputFile)
	log.Printf("Paperboy finished")
}

// fetchArticles fetches articles from a given feed URL
func fetchArticles(feedURL string) ([]Article, error) {
	fp := gofeed.NewParser()
	feed, err := fp.ParseURL(feedURL)
	if err != nil {
		return nil, fmt.Errorf("error fetching feed: %v", err)
	}

	var articles []Article
	for _, item := range feed.Items {
		// Parse publish date
		publishAt := item.PublishedParsed.UTC()
		articleDomain := getURLDomain(item.Link)

		articles = append(articles, Article{
			Title:     item.Title,
			URL:       item.Link,
			PublishAt: publishAt,
			URLDomain: articleDomain,
		})
	}

	return articles, nil
}

// generateMarkdown generates Markdown output with header and footer
func generateMarkdown(templateFile string, articlesByWeek map[string][]Article, weeks []string) string {
	// Read template file
	templateData, err := os.ReadFile(templateFile)
	if err != nil {
		log.Fatalf("Error reading template file: %v", err)
	}

	// Split template into header and footer sections
	templateParts := strings.SplitN(string(templateData), "---", 3)
	if len(templateParts) != 3 {
		log.Fatalf("Invalid template format")
	}

	header := strings.TrimSpace(templateParts[0])
	footer := strings.TrimSpace(templateParts[2])

	// Generate Markdown output
	var output strings.Builder
	output.WriteString(header)
	output.WriteString("\n\n")

	for _, week := range weeks {
		articles := articlesByWeek[week]
		if len(articles) == 0 {
			continue
		}

		// Sort articles by publish date
		slices.SortFunc(articles, func(a, b Article) int {
			return cmp.Compare(a.PublishAt.Unix(), b.PublishAt.Unix())
		})

		output.WriteString(fmt.Sprintf("## Week: %s\n\n", week))
		for _, article := range articles {
			output.WriteString(fmt.Sprintf("- %s @ %s: [%s](%s)\n", article.PublishAt.Format("2006-01-02"), article.URLDomain, article.Title, article.URL))
		}
		output.WriteString("\n")

	}

	output.WriteString(footer)
	output.WriteString("\n")

	return output.String()
}

// getURLDomain extracts the domain from a URL-like string
// e.g. "https://example.com" -> "example.com"
func getURLDomain(urlString string) string {
	urlString = strings.TrimSpace(urlString)

	if regexp.MustCompile(`^https?`).MatchString(urlString) {
		read, _ := url.Parse(urlString)
		urlString = read.Host
	}

	if regexp.MustCompile(`^www\.`).MatchString(urlString) {
		urlString = regexp.MustCompile(`^www\.`).ReplaceAllString(urlString, "")
	}

	return regexp.MustCompile(`([a-z0-9\-]+\.)+[a-z0-9\-]+`).FindString(urlString)
}
