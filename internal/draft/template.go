package draft

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// PromptVersion names the prompt below; it is recorded in every draft.
const PromptVersion = "draft-v1"

//go:embed prompts/draft-v1.md
var promptV1 string

// Section is one part of a template.
type Section struct {
	Heading  string
	Guidance string // what belongs here, from the template's comment
	Query    string // extra search words, from <!-- query: … -->
}

// DefaultTemplate is used when no --template is given.
const DefaultTemplate = `## Summary
<!-- What the practice achieves and when it applies, in two or three sentences. -->

## Before you start
<!-- Prerequisites, versions, access. query: prerequisites requirements install version access -->

## Recommended practices
<!-- The practices themselves, each with the reason. query: recommend should best practice avoid always -->

## Common mistakes
<!-- What goes wrong and how to recognise it. query: error fail mistake problem warning avoid -->

## Check that it works
<!-- How to verify the result. query: verify check status test confirm -->
`

var (
	tplHeading = regexp.MustCompile(`^##\s+(.+?)\s*$`)
	tplComment = regexp.MustCompile(`(?s)<!--(.*?)-->`)
	tplQuery   = regexp.MustCompile(`(?i)query:\s*(.+)$`)
)

// LoadTemplate reads a template file, or the default when path is empty. A
// template is Markdown: each "## " heading is a section, and an HTML comment
// under it holds guidance and, after "query:", extra search words.
func LoadTemplate(path string) ([]Section, error) {
	src := DefaultTemplate
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		src = string(b)
	}
	var out []Section
	var body []string
	flush := func() {
		if len(out) == 0 {
			return
		}
		s := &out[len(out)-1]
		for _, m := range tplComment.FindAllStringSubmatch(strings.Join(body, "\n"), -1) {
			g := strings.TrimSpace(m[1])
			if q := tplQuery.FindStringSubmatchIndex(g); q != nil {
				s.Query = strings.TrimSpace(g[q[2]:q[3]])
				g = strings.TrimSpace(g[:q[0]])
			}
			s.Guidance = strings.TrimSpace(s.Guidance + " " + g)
		}
		body = nil
	}
	for _, l := range strings.Split(src, "\n") {
		if m := tplHeading.FindStringSubmatch(l); m != nil {
			flush()
			out = append(out, Section{Heading: m[1]})
			continue
		}
		body = append(body, l)
	}
	flush()
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no \"## \" section headings", path)
	}
	return out, nil
}
