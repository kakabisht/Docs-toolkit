package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/docsbot/docsbot/internal/draft"
	"github.com/spf13/cobra"
)

func newDraftCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "draft",
		Short: "Draft a best-practice page with a citation for every statement, and record its review",
		Long: `Draft a best-practice page from a knowledge base and transcripts, then
review it claim by claim. Nothing is published: the draft goes to its own
folder with status: draft and review: required in its front matter.

  draft write    write draft.md, claims.json (the verification ledger) and review.md
  draft verify   record a reviewer's decision on claims; list what is left
  draft status   show what is left to review

Every statement in the draft cites the passage it comes from; statements
without a citation are marked [uncited]. The ledger lists, for every claim,
the numbers, versions, flags, commands, parameters, fields and defaults in it,
and whether each one appears in the cited passage. check fails (rule
draft-unverified-claims) on a page marked ready while its ledger has claims
that are not verified.

Without a model provider the draft is extractive: sentences come from the
sources as written. A provider (ollama, an OpenAI-compatible server, or
Anthropic) is set in the draft section of docsbot.yml. Source passages are only
sent to an endpoint off this machine when allow_remote is set.`,
	}
	c.AddCommand(newDraftWriteCmd(), newDraftVerifyCmd(), newDraftStatusCmd())
	return c
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func newDraftWriteCmd() *cobra.Command {
	var opt draft.Options
	var provider, model, endpoint string
	var allowRemote bool
	c := &cobra.Command{
		Use:   "write TOPIC",
		Short: "Write a cited draft from a knowledge base and transcripts",
		Args:  cobra.ExactArgs(1),
		Example: `  docsbot draft write "Upgrading lumen safely" --kb docs --transcript calls/upgrade.vtt
  docsbot draft write "Logging" --kb docs --transcript notes.txt --template templates/practice.md --out drafts/logging`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := textOrJSON(); err != nil {
				return err
			}
			cfg, err := loadConfig(opt.KB...)
			if err != nil {
				return err
			}
			pc := draft.ProviderConfig{Kind: cfg.Draft.Provider, Model: cfg.Draft.Model, Endpoint: cfg.Draft.Endpoint,
				APIKeyEnv: cfg.Draft.APIKeyEnv, AllowRemote: cfg.Draft.AllowRemote, Timeout: cfg.Draft.Timeout}
			if provider != "" {
				pc.Kind = provider
			}
			if model != "" {
				pc.Model = model
			}
			if endpoint != "" {
				pc.Endpoint = endpoint
			}
			pc.AllowRemote = pc.AllowRemote || allowRemote
			p, err := draft.NewProvider(pc)
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			opt.Provider = p
			opt.Topic = args[0]
			if opt.Template == "" {
				opt.Template = cfg.Draft.Template
			}
			if opt.Out == "" {
				opt.Out = filepath.Join("drafts", strings.Trim(slugRe.ReplaceAllString(strings.ToLower(opt.Topic), "-"), "-"))
			}
			opt.Date = time.Now().Format("2006-01-02")
			if !gf.quiet {
				opt.Progress = func(s string) { fmt.Fprintln(os.Stderr, s) }
			}
			res, err := draft.Generate(cmd.Context(), opt)
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			w, done, err := out()
			if err != nil {
				return err
			}
			defer done()
			if gf.format == "json" {
				return writeJSON(w, res)
			}
			fmt.Fprintf(w, "Draft written to %s (%s).\n", res.Dir, strings.Join(res.Files, ", "))
			fmt.Fprintf(w, "%d claims from %d source passages; %d without a citation.\n", total(res.Counts), res.Sources, res.Counts["uncited"])
			if len(res.Empty) > 0 {
				fmt.Fprintf(w, "No source for: %s (placeholders left in the draft).\n", strings.Join(res.Empty, ", "))
			}
			fmt.Fprintf(w, "Next: review the claims in %s and record each with docsbot draft verify %s.\n", filepath.Join(res.Dir, draft.ReviewFile), res.Dir)
			return nil
		},
	}
	f := c.Flags()
	f.StringSliceVar(&opt.KB, "kb", nil, "knowledge base: folders or files of Markdown, AsciiDoc, reST or text (repeatable)")
	f.StringSliceVar(&opt.Transcripts, "transcript", nil, "local transcript: .vtt, .srt or .txt (repeatable)")
	f.StringVar(&opt.Template, "template", "", "Markdown template: \"## \" headings with an optional <!-- guidance. query: words --> comment")
	f.StringVar(&opt.Title, "title", "", "page title (default: the topic)")
	f.StringVar(&opt.Out, "out", "", "output folder (default drafts/<topic>); never inside the knowledge base")
	f.IntVar(&opt.PerSection, "passages", 5, "source passages per section")
	f.BoolVar(&opt.Force, "force", false, "replace an existing draft and its review record")
	f.StringVar(&provider, "provider", "", "model provider: none, ollama, openai, anthropic (overrides the config)")
	f.StringVar(&model, "model", "", "model name (overrides the config)")
	f.StringVar(&endpoint, "endpoint", "", "provider endpoint (overrides the config)")
	f.BoolVar(&allowRemote, "allow-remote", false, "allow sending source passages to an endpoint off this machine")
	return c
}

func total(m map[string]int) int {
	return m[draft.Verified] + m[draft.Unverified] + m[draft.NeedsChange] + m[draft.Rejected]
}

// syncDraft reads a draft folder and matches its ledger to draft.md.
func syncDraft(dir string) (*draft.Ledger, draft.SyncResult, error) {
	led, err := draft.ReadLedger(filepath.Join(dir, draft.LedgerFile))
	if err != nil {
		return nil, draft.SyncResult{}, err
	}
	md, err := os.ReadFile(filepath.Join(dir, led.Draft))
	if err != nil {
		return nil, draft.SyncResult{}, err
	}
	return led, led.Sync(string(md)), nil
}

func saveDraft(dir string, led *draft.Ledger) error {
	if err := led.Write(filepath.Join(dir, draft.LedgerFile)); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, draft.ReviewFile), []byte(draft.Checklist(led)), 0o644)
}

func newDraftVerifyCmd() *cobra.Command {
	var claims []string
	var status, note, by string
	c := &cobra.Command{
		Use:   "verify DIR",
		Short: "Record a review decision on claims",
		Long: `Record a reviewer's decision on one or more claims, then rewrite review.md.
Before anything else the ledger is matched to draft.md: edited or new
statements become unverified claims, and claims whose text is gone are marked
removed. With no --claim, it only does that and lists what is left.

A claim without a citation can only be verified with a --note that says where
it was checked.`,
		Args: cobra.ExactArgs(1),
		Example: `  docsbot draft verify drafts/logging --claim C1,C2 --status verified --by "A Reviewer"
  docsbot draft verify drafts/logging --claim C5 --status needs-change --note "the default is info"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := textOrJSON(); err != nil {
				return err
			}
			dir := args[0]
			led, sync, err := syncDraft(dir)
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			switch status {
			case draft.Verified, draft.Rejected, draft.NeedsChange, draft.Unverified:
			default:
				return &exitError{code: ExitError, msg: "error: --status must be verified, rejected, needs-change or unverified"}
			}
			if len(claims) > 0 && by == "" {
				by = os.Getenv("USER")
			}
			for _, id := range claims {
				cl := led.Find(strings.TrimSpace(id))
				if cl == nil || cl.Removed {
					return &exitError{code: ExitError, msg: fmt.Sprintf("error: no claim %s in the current draft", id)}
				}
				if status == draft.Verified && cl.Uncited && note == "" {
					return &exitError{code: ExitError, msg: fmt.Sprintf("error: %s has no citation; give --note with where you checked it, or add a citation to the draft", cl.ID)}
				}
				cl.Status, cl.Reviewer, cl.Note = status, by, note
				cl.Reviewed = time.Now().Format("2006-01-02")
			}
			if err := saveDraft(dir, led); err != nil {
				return err
			}
			return printDraftStatus(led, sync)
		},
	}
	c.Flags().StringSliceVar(&claims, "claim", nil, "claim ids, e.g. C3 or C3,C4")
	c.Flags().StringVar(&status, "status", draft.Verified, "verified, rejected, needs-change or unverified")
	c.Flags().StringVar(&note, "note", "", "what was checked, or what must change")
	c.Flags().StringVar(&by, "by", "", "reviewer name (default $USER)")
	return c
}

func newDraftStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status DIR",
		Short: "Show what is left to review in a draft",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := textOrJSON(); err != nil {
				return err
			}
			led, sync, err := syncDraft(args[0])
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			return printDraftStatus(led, sync)
		},
	}
}

func printDraftStatus(led *draft.Ledger, sync draft.SyncResult) error {
	w, done, err := out()
	if err != nil {
		return err
	}
	defer done()
	problems := led.Problems()
	if gf.format == "json" {
		return writeJSON(w, map[string]any{"counts": led.Counts(), "changed_since_last_run": sync, "open": problems})
	}
	c := led.Counts()
	fmt.Fprintf(w, "%d claims: %d verified, %d unverified, %d needing change, %d rejected; %d without a citation.\n",
		total(c), c[draft.Verified], c[draft.Unverified], c[draft.NeedsChange], c[draft.Rejected], c["uncited"])
	if n := len(sync.Added) + len(sync.Changed) + len(sync.Removed); n > 0 {
		fmt.Fprintf(w, "draft.md changed: %d new or edited (%s), %d removed.\n", len(sync.Added)+len(sync.Changed),
			strings.Join(append(append([]string{}, sync.Added...), sync.Changed...), ", "), len(sync.Removed))
	}
	if len(problems) == 0 {
		fmt.Fprintln(w, "Every claim is verified. Set status: ready in the front matter when the rest of the review is done.")
		return nil
	}
	for i, p := range problems {
		if i == 20 {
			fmt.Fprintf(w, "… and %d more (see review.md)\n", len(problems)-20)
			break
		}
		fmt.Fprintln(w, "  "+p)
	}
	return nil
}
