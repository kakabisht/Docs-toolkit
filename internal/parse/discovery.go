package parse

import (
	"bytes"
	"encoding/xml"
	"regexp"
	"strings"
)

var mdLink = regexp.MustCompile(`\[[^\]]*\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)

// LLMSTxtLinks returns the link targets listed in an llms.txt file.
func LLMSTxtLinks(src []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mdLink.FindAllSubmatch(src, -1) {
		h := string(m[1])
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// Sitemap parses a sitemap or sitemap index. It returns page URLs and nested sitemap URLs.
func Sitemap(src []byte) (pages, sitemaps []string) {
	var doc struct {
		XMLName xml.Name
		URLs    []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
		Maps []struct {
			Loc string `xml:"loc"`
		} `xml:"sitemap"`
	}
	if err := xml.NewDecoder(bytes.NewReader(src)).Decode(&doc); err != nil {
		return nil, nil
	}
	for _, u := range doc.URLs {
		if l := strings.TrimSpace(u.Loc); l != "" {
			pages = append(pages, l)
		}
	}
	for _, s := range doc.Maps {
		if l := strings.TrimSpace(s.Loc); l != "" {
			sitemaps = append(sitemaps, l)
		}
	}
	return pages, sitemaps
}

// RobotsSitemaps returns Sitemap: entries from robots.txt.
func RobotsSitemaps(src []byte) []string {
	var out []string
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if len(line) > 8 && strings.EqualFold(line[:8], "sitemap:") {
			out = append(out, strings.TrimSpace(line[8:]))
		}
	}
	return out
}
