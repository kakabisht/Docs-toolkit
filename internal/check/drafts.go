package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kakabisht/Docs-toolkit/internal/draft"
)

// statuses that say a page is done with review
var readyStatus = map[string]bool{"ready": true, "approved": true, "final": true, "published": true, "done": true}

// drafts fails a page marked ready while its claims ledger (front matter
// claims:) has claims that are not verified. The ledger is matched to the
// page first, so statements edited since review count as unverified.
func (c *checker) drafts() {
	for _, p := range c.s.Pages {
		if p.Err != "" || p.RepoPath == "" || !readyStatus[p.DocStatus] {
			continue
		}
		ref, _ := p.FrontMatter["claims"].(string)
		if ref == "" {
			continue
		}
		f := fileOf(p)
		line := keyLine(p.Raw, "claims")
		path := filepath.Join(c.s.Root, filepath.Dir(filepath.FromSlash(p.RepoPath)), filepath.FromSlash(ref))
		led, err := draft.ReadLedger(path)
		if err != nil {
			if os.IsNotExist(err) {
				err = fmt.Errorf("%s not found", ref)
			}
			c.e.Add("draft-unverified-claims", f, line, 0, ref, "Page is marked "+p.DocStatus+" but its claims ledger can't be read: "+err.Error(), "Restore the ledger, or remove claims: if the page no longer comes from a draft.")
			continue
		}
		led.Sync(string(p.Raw))
		problems := led.Problems()
		if len(problems) == 0 {
			continue
		}
		show := problems
		if len(show) > 5 {
			show = append(show[:5:5], fmt.Sprintf("and %d more", len(problems)-5))
		}
		c.e.Add("draft-unverified-claims", f, keyLine(p.Raw, "status"), 0, ref,
			fmt.Sprintf("Page is marked %s but has open claims (%d): %s", p.DocStatus, len(problems), strings.Join(show, "; ")),
			"Review them with `docsbot draft verify`, or set the status back to draft.")
	}
}
