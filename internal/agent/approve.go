package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kakabisht/Docs-toolkit/internal/draft"
)

// Approval is approval.json: who approved which version of the draft.
type Approval struct {
	By          string `json:"by"`
	Date        string `json:"date"`
	DraftSHA256 string `json:"draft_sha256"` // draft.md as approved
	Score       int    `json:"score"`
	Claims      int    `json:"claims_verified"`
	Published   string `json:"published,omitempty"` // page written by Publish
	// Warnings the evaluation raised that the approver accepted.
	Warnings []string `json:"accepted_warnings,omitempty"`
}

// Approve records a person's approval. It is refused unless the evaluation
// is of the current draft.md, the gate passed, and every claim is verified
// (docsbot draft verify). It sets status: approved in the draft's front
// matter; `docsbot check` then keeps failing the page if a claim is edited
// back to unverified.
func Approve(dir, by, date string) (*Approval, error) {
	if strings.TrimSpace(by) == "" {
		return nil, fmt.Errorf("give the approver's name with --by")
	}
	mdPath := filepath.Join(dir, draft.DraftFile)
	raw, err := os.ReadFile(mdPath)
	if err != nil {
		return nil, err
	}
	// approving again (for example to publish) keeps the first approval
	if b, err := os.ReadFile(filepath.Join(dir, ApprovalFile)); err == nil {
		var prev Approval
		if json.Unmarshal(b, &prev) == nil && prev.DraftSHA256 == sha(raw) {
			return &prev, nil
		}
	}
	var e Eval
	b, err := os.ReadFile(filepath.Join(dir, EvalFile))
	if err != nil {
		return nil, fmt.Errorf("%s has no %s; run docsbot agent eval %s first", dir, EvalFile, dir)
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	if e.DraftSHA256 != sha(raw) {
		return nil, fmt.Errorf("draft.md changed since it was evaluated; run docsbot agent eval %s and approve again", dir)
	}
	if !e.Gate.Passed {
		return nil, fmt.Errorf("the evaluation gate is blocked: %s (see %s)", strings.Join(e.Gate.Blocking, "; "), filepath.Join(dir, EvalReport))
	}
	led, err := draft.ReadLedger(filepath.Join(dir, draft.LedgerFile))
	if err != nil {
		return nil, err
	}
	led.Sync(string(raw))
	if p := led.Problems(); len(p) > 0 {
		show := p
		if len(show) > 5 {
			show = append(show[:5:5], fmt.Sprintf("and %d more", len(p)-5))
		}
		return nil, fmt.Errorf("%d claim(s) are not verified: %s. Record each with docsbot draft verify %s", len(p), strings.Join(show, "; "), dir)
	}
	approved := setFrontMatter(string(raw), map[string]string{"status": "approved", "review": "done", "approved_by": by, "approved": date})
	if err := os.WriteFile(mdPath, []byte(approved), 0o644); err != nil {
		return nil, err
	}
	// the ledger matches claims by text, so the front matter change needs no re-review
	a := &Approval{By: by, Date: date, DraftSHA256: sha([]byte(approved)), Score: e.Score, Claims: led.Counts()[draft.Verified], Warnings: e.Gate.Warnings}
	return a, writeJSON(filepath.Join(dir, ApprovalFile), a)
}

// Publish writes an approved new page into the docs at the group's target,
// without the review apparatus: citations, footnotes and review comments are
// taken out, and so are the draft's front matter fields. Pages to update are
// never overwritten; their changes are applied by hand from proposed-changes.md.
func Publish(dir string, force bool) (string, error) {
	var a Approval
	b, err := os.ReadFile(filepath.Join(dir, ApprovalFile))
	if err != nil {
		return "", fmt.Errorf("%s is not approved; run docsbot agent approve %s --by NAME first", dir, dir)
	}
	if err := json.Unmarshal(b, &a); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(dir, draft.DraftFile))
	if err != nil {
		return "", err
	}
	if sha(raw) != a.DraftSHA256 {
		return "", fmt.Errorf("draft.md changed after approval; evaluate and approve it again")
	}
	meta, err := LoadMeta(dir)
	if err != nil {
		return "", err
	}
	if meta.Group.Mode != "new" {
		return "", fmt.Errorf("this draft updates %s, which already exists; apply %s to it by hand", meta.Group.Target, filepath.Join(dir, ChangesFile))
	}
	target := filepath.Join(meta.Root, filepath.FromSlash(meta.Group.Target))
	if _, err := os.Stat(target); err == nil && !force {
		return "", fmt.Errorf("%s already exists; use --force to replace it", target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(target, []byte(Clean(string(raw))), 0o644); err != nil {
		return "", err
	}
	a.Published = meta.Group.Target
	return target, writeJSON(filepath.Join(dir, ApprovalFile), a)
}

var (
	citeMarker   = regexp.MustCompile(`\[\^s\d+\]`)
	footnoteDef  = regexp.MustCompile(`^\[\^s\d+\]:`)
	reviewCmt    = regexp.MustCompile(`^<!-- .* -->$`)
	uncitedMark  = regexp.MustCompile(`\s*\*\*\[uncited\]\*\*`)
	sourceLine   = regexp.MustCompile(`^Source:\s*$`)
	reviewFields = map[string]bool{"status": true, "review": true, "topic": true, "generator": true, "prompt_version": true,
		"generated": true, "claims": true, "approved_by": true, "approved": true}
)

// Clean turns an approved draft into a publishable page.
func Clean(md string) string {
	fm, body := splitFrontMatter(md)
	var keep []string
	for _, l := range strings.Split(fm, "\n") {
		k, _, _ := strings.Cut(l, ":")
		if !reviewFields[strings.TrimSpace(k)] && strings.TrimSpace(l) != "" {
			keep = append(keep, l)
		}
	}
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if footnoteDef.MatchString(l) || reviewCmt.MatchString(strings.TrimSpace(l)) {
			continue
		}
		l = uncitedMark.ReplaceAllString(citeMarker.ReplaceAllString(l, ""), "")
		if sourceLine.MatchString(l) {
			continue
		}
		out = append(out, l)
	}
	text := strings.Join(out, "\n")
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")
	page := strings.TrimSpace(text) + "\n"
	if len(keep) > 0 {
		page = "---\n" + strings.Join(keep, "\n") + "\n---\n\n" + page
	}
	return page
}

func splitFrontMatter(md string) (string, string) {
	if !strings.HasPrefix(md, "---\n") {
		return "", md
	}
	end := strings.Index(md[4:], "\n---\n")
	if end < 0 {
		return "", md
	}
	return md[4 : 4+end], md[4+end+5:]
}

// setFrontMatter replaces or adds simple key: value fields.
func setFrontMatter(md string, set map[string]string) string {
	fm, body := splitFrontMatter(md)
	lines := strings.Split(fm, "\n")
	done := map[string]bool{}
	for i, l := range lines {
		k, _, ok := strings.Cut(l, ":")
		if v, want := set[strings.TrimSpace(k)]; ok && want {
			lines[i] = fmt.Sprintf("%s: %q", strings.TrimSpace(k), v)
			done[strings.TrimSpace(k)] = true
		}
	}
	for _, k := range []string{"status", "review", "approved_by", "approved"} {
		if v, ok := set[k]; ok && !done[k] {
			lines = append(lines, fmt.Sprintf("%s: %q", k, v))
		}
	}
	return "---\n" + strings.Join(lines, "\n") + "\n---\n" + body
}
