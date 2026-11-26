package parse

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Anchor styles, matching how each site generator turns headings into IDs.
const (
	StyleGitHub   = "github"   // GitHub, Hugo, Docusaurus, Astro/Starlight, Mintlify
	StyleMkDocs   = "mkdocs"   // Python-Markdown toc extension
	StyleSphinx   = "sphinx"   // docutils make_id
	StyleKramdown = "kramdown" // Jekyll
)

// Slugify turns heading text into a GitHub-style anchor: lowercase,
// punctuation dropped, each space becomes a hyphen.
func Slugify(s string) string { return SlugifyStyle(s, StyleGitHub) }

var (
	mkdocsStrip = regexp.MustCompile(`[^\w\s-]`)
	mkdocsJoin  = regexp.MustCompile(`[-\s]+`)
	sphinxNon   = regexp.MustCompile(`[^a-z0-9]+`)
)

// SlugifyStyle slugs s with the named style (unknown styles use GitHub's).
func SlugifyStyle(s, style string) string {
	switch style {
	case StyleMkDocs:
		s = asciiFold(s)
		s = strings.ToLower(strings.TrimSpace(mkdocsStrip.ReplaceAllString(s, "")))
		return mkdocsJoin.ReplaceAllString(s, "-")
	case StyleSphinx:
		s = sphinxNon.ReplaceAllString(strings.ToLower(asciiFold(s)), "-")
		s = strings.Trim(s, "-")
		return strings.TrimLeft(s, "0123456789-")
	case StyleKramdown:
		var b strings.Builder
		for _, r := range strings.ToLower(strings.TrimSpace(s)) {
			switch {
			case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_'):
				b.WriteRune(r)
			case r == ' ':
				b.WriteRune('-')
			}
		}
		return strings.TrimLeft(b.String(), "0123456789-_")
	}
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune('-')
		}
	}
	return b.String()
}

func asciiFold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(s) {
		if r < 128 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// slugger hands out unique anchors: foo, foo-1, foo-2 (foo_1 for MkDocs).
type slugger struct {
	seen  map[string]int
	style string
}

func newSlugger(style string) *slugger { return &slugger{seen: map[string]int{}, style: style} }

func (s *slugger) slug(text string) string { return s.unique(SlugifyStyle(text, s.style)) }

func (s *slugger) reserve(id string) { s.seen[id]++ }

func (s *slugger) unique(base string) string {
	sep := "-"
	if s.style == StyleMkDocs {
		sep = "_"
	}
	if base == "" && s.style == StyleSphinx {
		base = "id"
	}
	n, seen := s.seen[base]
	s.seen[base] = n + 1
	if !seen {
		return base
	}
	for {
		cand := base + sep + strconv.Itoa(n)
		if _, taken := s.seen[cand]; !taken {
			s.seen[cand] = 1
			return cand
		}
		n++
	}
}
