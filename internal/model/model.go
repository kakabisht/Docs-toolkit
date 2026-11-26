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
	Column  int    `json:"column,omitempty"`
	From    string `json:"from,omitempty"`    // file the link was written in, when it came from an include
	Section string `json:"section,omitempty"` // anchor of the heading the link sits under
	Image   bool   `json:"image,omitempty"`
	Nav     bool   `json:"nav,omitempty"` // site navigation (header, sidebar, footer), not page content
	// FromModule is set for MDX imports, whose relative links resolve from the
	// partial's own folder; inlined includes resolve from the including page.
	FromModule bool `json:"-"`
}

// CodeBlock is a fenced or <pre> code block.
type CodeBlock struct {
	Lang    string `json:"lang"`
	Lines   int    `json:"lines"`
	Line    int    `json:"line,omitempty"`
	Content string `json:"-"`
	Fenced  bool   `json:"fenced,omitempty"` // fenced or delimited, so a language can be declared
}

// Include is a file pulled into a page (include shortcode, MDX import, AsciiDoc include::).
type Include struct {
	Target string `json:"target"`
	Line   int    `json:"line,omitempty"`
	Kind   string `json:"kind"`           // shortcode | import | include
	Path   string `json:"path,omitempty"` // resolved path relative to the repository root, when found
}

// Span is a piece of inline text with its location (inline code, table cells).
type Span struct {
	Text string `json:"text"`
	Line int    `json:"line,omitempty"`
}

// Paragraph is a block of prose under a heading.
type Paragraph struct {
	Text    string `json:"text"`
	Line    int    `json:"line,omitempty"`
	Section string `json:"section,omitempty"`
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

	// Local-repository fields.
	RepoPath       string         `json:"repo_path,omitempty"` // path relative to the repository root
	FrontMatter    map[string]any `json:"front_matter,omitempty"`
	FrontMatterErr string         `json:"front_matter_error,omitempty"`
	FrontMatterEnd int            `json:"-"` // last line of the front matter block
	Tags           []string       `json:"tags,omitempty"`
	Type           string         `json:"type,omitempty"` // content type
	Audience       []string       `json:"audience,omitempty"`
	Owner          string         `json:"owner,omitempty"`
	Version        string         `json:"version,omitempty"`
	DocStatus      string         `json:"doc_status,omitempty"`    // front matter status (draft, ready ...)
	LastModified   int64          `json:"last_modified,omitempty"` // unix seconds, from git
	Includes       []Include      `json:"includes,omitempty"`
	InlineCode     []Span         `json:"-"`
	TableCells     []Span         `json:"-"` // first-column cells of tables
	Paragraphs     []Paragraph    `json:"-"`
	DefTerms       []Span         `json:"-"` // definition-list terms
	Raw            []byte         `json:"-"`
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
