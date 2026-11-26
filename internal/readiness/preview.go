package readiness

import (
	"sort"
	"strings"
)

// RepoChecks are the afdocs checks whose result depends on what the
// repository contains: its llms.txt and the Markdown of its pages. Every
// other check depends on the host, the theme or the server (HTML size,
// content negotiation, status codes, caching, rendering, authentication), so
// a local preview can't answer it. Unknown checks count as host checks.
var RepoChecks = map[string]bool{
	"llms-txt-exists": true, "llms-txt-valid": true, "llms-txt-size": true, "llms-txt-links-resolve": true,
	"llms-txt-directive-md": true, "llms-txt-coverage": true,
	"page-size-markdown": true, "tabbed-content-serialization": true, "section-header-quality": true,
	"markdown-code-fence-validity": true, "markdown-link-portability": true, "embedded-data-serialization": true,
}

// HostNote is the message on a check left out of a local preview.
const HostNote = "depends on the host, not the repository: check the live site with --site"

// Preview turns an afdocs run against docsbot's local preview into a result
// about the repository: host checks become skipped, drop out of the score,
// and can't cap it. It returns the ids it left out.
func (af *AfdocsResult) Preview() []string {
	var out []string
	for i := range af.Results {
		r := &af.Results[i]
		if RepoChecks[r.ID] {
			continue
		}
		out = append(out, r.ID)
		switch r.Status {
		case "pass":
			af.Summary.Pass--
		case "warn":
			af.Summary.Warn--
		case "fail":
			af.Summary.Fail--
		case "error":
			af.Summary.Error--
		case "skip":
			af.Summary.Skip--
		}
		af.Summary.Skip++
		r.Status = "skip"
		r.Message = HostNote
	}
	if s := af.Scoring; s != nil {
		for id, cs := range s.CheckScores {
			if !RepoChecks[id] {
				cs.ScoreDisplayMode = "notApplicable"
				s.CheckScores[id] = cs
			}
		}
		if s.Cap != nil && !RepoChecks[s.Cap.CheckID] {
			s.Cap = nil
		}
		for id := range s.Resolutions {
			if !RepoChecks[id] {
				delete(s.Resolutions, id)
			}
		}
		// afdocs' own totals include host checks; RawScore recomputes them
		if len(s.CheckScores) > 0 {
			s.Overall = s.pointsScore()
		}
		s.Grade = ""
	}
	sort.Strings(out)
	return out
}

// GeneratorMakesLLMS reports whether a site's configuration shows a plugin
// that writes llms.txt at build time, so its absence from the repo is not a
// problem with the published site.
func GeneratorMakesLLMS(files map[string]string) string {
	for name, body := range files {
		b := strings.ToLower(body)
		switch {
		case strings.HasSuffix(name, "mkdocs.yml") && strings.Contains(b, "llmstxt"):
			return "the mkdocs-llmstxt plugin"
		case strings.HasSuffix(name, "package.json") && strings.Contains(b, "llms"):
			return "an llms.txt plugin in package.json"
		case (strings.HasSuffix(name, "hugo.toml") || strings.HasSuffix(name, "config.toml") || strings.HasSuffix(name, "hugo.yaml")) && strings.Contains(b, "llms"):
			return "an llms output format in the Hugo config"
		}
	}
	return ""
}
