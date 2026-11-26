package finding

import "sort"

// Category groups rules for `check --only`.
type Category string

const (
	Links     Category = "links"
	Structure Category = "structure"
	Metadata  Category = "metadata"
	Images    Category = "images"
	Drafts    Category = "drafts"
)

// Rule describes one check.
type Rule struct {
	ID       string   `json:"id"`
	Category Category `json:"category"`
	Severity Severity `json:"default_severity"`
	Summary  string   `json:"summary"`
}

// Rules is the registry of every rule, keyed by ID.
var Rules = map[string]Rule{}

func reg(id string, c Category, s Severity, summary string) {
	Rules[id] = Rule{ID: id, Category: c, Severity: s, Summary: summary}
}

func init() {
	// internal links
	reg("link-missing-file", Links, Error, "Link points to a page or file that does not exist")
	reg("link-missing-anchor", Links, Error, "Link points to a #section that does not exist on the target page")
	reg("link-case-mismatch", Links, Error, "Link only resolves on case-insensitive file systems (macOS, Windows), not on Linux")
	reg("link-not-in-nav", Links, Warning, "Link points to a page that is not in the navigation")
	reg("link-redirect", Links, Warning, "Link goes through a redirect or alias; link to the canonical page")
	reg("link-empty", Links, Warning, "Link has an empty target")
	reg("page-orphan", Links, Warning, "No page or navigation entry links to this page")
	reg("nav-missing-target", Links, Error, "Navigation entry points to a page that does not exist")
	reg("page-duplicate-slug", Links, Error, "Two files publish to the same URL")
	// external links
	reg("link-external-broken", Links, Error, "External URL returns 404 or 410")
	reg("link-external-server-error", Links, Warning, "External URL returns a server error (5xx) after a retry")
	reg("link-external-redirect", Links, Info, "External URL permanently redirects; update the link")
	reg("link-external-timeout", Links, Warning, "External URL timed out or could not be reached")
	reg("link-external-unverified", Links, Info, "External site refused an automated check (401, 403 or 429); verify by hand")
	reg("link-external-anchor", Links, Warning, "External page exists but the #anchor was not found on it")
	// images
	reg("image-missing", Images, Error, "Image file does not exist")
	reg("image-missing-alt", Images, Warning, "Image has no alt text")
	reg("image-case-mismatch", Images, Error, "Image path only resolves on case-insensitive file systems")
	// structure
	reg("heading-skipped-level", Structure, Warning, "Heading skips a level (for example H2 to H4)")
	reg("heading-multiple-h1", Structure, Warning, "Page has more than one H1 (the title counts as the H1)")
	reg("heading-empty", Structure, Error, "Heading has no text")
	reg("title-duplicate", Structure, Warning, "Another page has the same title")
	reg("code-missing-language", Structure, Warning, "Fenced code block has no language")
	reg("content-todo", Structure, Warning, "Leftover TODO, FIXME or placeholder marker")
	reg("include-missing", Structure, Error, "Included or imported file does not exist")
	// metadata
	reg("frontmatter-invalid", Metadata, Error, "Front matter does not parse")
	reg("frontmatter-schema", Metadata, Error, "Front matter does not match the configured JSON Schema")
	reg("frontmatter-missing-title", Metadata, Warning, "Page has no title in front matter or an H1")
	reg("tag-not-in-vocabulary", Metadata, Warning, "Tag is not in the controlled vocabulary")
	reg("tag-alias", Metadata, Warning, "Tag is an alias; use the preferred term")
	// drafts
	reg("draft-unverified-claims", Drafts, Error, "Draft marked ready still has unverified claims")
}

// RuleIDs returns every rule ID in order.
func RuleIDs() []string {
	ids := make([]string, 0, len(Rules))
	for id := range Rules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Policy applies severity overrides and category filters.
type Policy struct {
	Overrides map[string]Severity
	Only      map[Category]bool // empty means all categories
}

// Enabled reports whether rule id runs, and at which severity.
func (p Policy) Enabled(id string) (Severity, bool) {
	r, ok := Rules[id]
	if !ok {
		return Warning, true
	}
	if len(p.Only) > 0 && !p.Only[r.Category] {
		return Off, false
	}
	sev := r.Severity
	if o, ok := p.Overrides[id]; ok {
		sev = o
	}
	return sev, sev != Off
}

// Emitter collects findings through a Policy.
type Emitter struct {
	Policy   Policy
	Findings []Finding
	// Filter, when set, keeps only findings in these files (--changed-only).
	Filter map[string]bool
}

// Add records a finding for rule id unless the rule is off or filtered.
func (e *Emitter) Add(id, file string, line, col int, target, msg, hint string) {
	sev, ok := e.Policy.Enabled(id)
	if !ok {
		return
	}
	if e.Filter != nil && !e.Filter[file] {
		return
	}
	e.Findings = append(e.Findings, Finding{Rule: id, Severity: sev, File: file, Line: line, Column: col, Target: target, Message: msg, Hint: hint})
}

// Result sorts and dedupes the collected findings.
func (e *Emitter) Result() []Finding {
	fs := Dedupe(e.Findings)
	Sort(fs)
	return fs
}
