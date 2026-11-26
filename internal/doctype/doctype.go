// Package doctype classifies pages into Diátaxis content types: tutorial,
// how-to, reference and explanation. Front matter wins; otherwise documented
// heuristics guess, and the guess is labelled as inferred.
package doctype

import (
	"path"
	"regexp"
	"strings"

	"github.com/docsbot/docsbot/internal/model"
)

// The four Diátaxis types, plus landing pages.
const (
	Tutorial    = "tutorial"
	HowTo       = "how-to"
	Reference   = "reference"
	Explanation = "explanation"
	Landing     = "landing"
)

// All lists the content types in display order.
var All = []string{Tutorial, HowTo, Reference, Explanation}

// Normalize maps synonyms to the four types.
func Normalize(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "tutorial", "tutorials", "getting-started", "quickstart", "quick-start":
		return Tutorial
	case "how-to", "howto", "how-to-guide", "guide", "task", "troubleshooting":
		return HowTo
	case "reference", "api", "cli", "release-notes":
		return Reference
	case "explanation", "concept", "conceptual", "overview", "architecture", "background":
		return Explanation
	case "landing", "index":
		return Landing
	}
	return strings.ToLower(strings.TrimSpace(t))
}

// Overrides maps path globs to content types (from configuration).
type Overrides map[string]string

var (
	howtoVerbs = regexp.MustCompile(`(?i)^(how to|install|installing|configure|configuring|set up|setting up|create|creating|deploy|deploying|add|adding|use|using|migrate|migrating|upgrade|upgrading|enable|disable|connect|run|manage|managing|troubleshoot|troubleshooting|monitor|build|debug|integrate|send|import|export|update|remove|delete)\b`)
	refWords   = regexp.MustCompile(`(?i)\b(reference|api|cli|commands?|options|settings|parameters|flags|glossary|functions?|methods?|variables|schema|specification|changelog|release notes)\b`)
	expWords   = regexp.MustCompile(`(?i)\b(overview|introduction|concepts?|architecture|about|explanation|understanding|why|what is|how .+ works|design|background)\b`)
	tutWords   = regexp.MustCompile(`(?i)\b(tutorial|getting started|quick ?start|first steps|walkthrough|hands-on|learn)\b`)
)

// Classify returns the page's content type and whether it was inferred.
func Classify(p *model.Page, ov Overrides, match func(glob, rel string) bool) (string, bool) {
	if p.Type != "" {
		return Normalize(p.Type), false
	}
	rel := strings.TrimPrefix(p.Key, "/")
	for glob, t := range ov {
		if match(glob, rel) || (p.Path != "" && match(glob, p.Path)) {
			return Normalize(t), false
		}
	}
	segs := strings.ToLower(p.Key)
	title := strings.TrimSpace(p.Title)
	base := strings.ToLower(strings.TrimSuffix(path.Base(p.Path), path.Ext(p.Path)))
	switch {
	case p.Key == "/":
		return Landing, true
	case strings.Contains(segs, "tutorial") || tutWords.MatchString(title):
		return Tutorial, true
	case strings.Contains(segs, "/reference") || strings.Contains(segs, "/api") || strings.Contains(segs, "/cli") || strings.Contains(segs, "/commands") || refWords.MatchString(title) || referenceShaped(p):
		return Reference, true
	case strings.Contains(segs, "how-to") || strings.Contains(segs, "/guides/") || howtoVerbs.MatchString(title) || steps(p) >= 3:
		return HowTo, true
	case strings.Contains(segs, "concept") || strings.Contains(segs, "/about") || strings.Contains(segs, "overview") || expWords.MatchString(title) || base == "introduction":
		return Explanation, true
	case (base == "_index" || base == "index" || base == "readme") && len(p.Paragraphs) <= 3:
		return Landing, true
	}
	return Explanation, true
}

// referenceShaped is true for pages dominated by tables, definition lists or code identifiers.
func referenceShaped(p *model.Page) bool {
	structured := len(p.TableCells) + len(p.DefTerms)
	return structured >= 8 && structured >= len(p.Paragraphs)
}

var stepRe = regexp.MustCompile(`(?i)^(step \d+|\d+\.)`)

// steps counts numbered-step headings.
func steps(p *model.Page) int {
	n := 0
	for _, h := range p.Headings {
		if stepRe.MatchString(h.Text) {
			n++
		}
	}
	return n
}
