// Package model holds the types shared by the crawler, graph, link checker and audit.
package model

// Heading is a section heading inside a page.
type Heading struct {
	Level  int    `json:"level"`
	Text   string `json:"text"`
	Anchor string `json:"anchor"`
	Line   int    `json:"line,omitempty"`
}

// Link is a hyperlink (or image reference) found in a page.
type Link struct {
	Href    string `json:"href"`
	Text    string `json:"text,omitempty"`
	Line    int    `json:"line,omitempty"`
	Section string `json:"section,omitempty"` // anchor of the heading the link sits under
	Image   bool   `json:"image,omitempty"`
	Nav     bool   `json:"nav,omitempty"` // site navigation (header, sidebar, footer), not page content
}

// CodeBlock is a fenced or <pre> code block.
type CodeBlock struct {
	Lang  string `json:"lang"`
	Lines int    `json:"lines"`
	Line  int    `json:"line,omitempty"`
}

// Page is one parsed docs page, from a live site or a local repo.
type Page struct {
	Key         string          `json:"key"`            // canonical id used for graph nodes and link matching
	URL         string          `json:"url,omitempty"`  // absolute URL, when known
	Path        string          `json:"path,omitempty"` // file path relative to the content root (local mode)
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Format      string          `json:"format"` // markdown | html
	Headings    []Heading       `json:"headings"`
	Anchors     map[string]bool `json:"-"`
	Links       []Link          `json:"links"`
	CodeBlocks  []CodeBlock     `json:"code_blocks"`
	Words       int             `json:"words"`
	Chars       int             `json:"chars"`
	Status      int             `json:"status,omitempty"`
	FinalURL    string          `json:"final_url,omitempty"`
	Source      string          `json:"source"` // llms.txt | sitemap | crawl | local
	Aliases     []string        `json:"aliases,omitempty"`
	Draft       bool            `json:"draft,omitempty"`
	Err         string          `json:"error,omitempty"`
}

// Redirected reports whether fetching the page ended at a different URL.
func (p *Page) Redirected() bool { return p.FinalURL != "" && p.FinalURL != p.URL }

// LLMSTxt describes the llms.txt file of a docs source.
type LLMSTxt struct {
	Location string   `json:"location"`
	Found    bool     `json:"found"`
	Size     int      `json:"size"`
	Links    []string `json:"links"` // hrefs listed in the file
}
