package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kakabisht/Docs-toolkit/internal/agent"
	"github.com/kakabisht/Docs-toolkit/internal/config"
	"github.com/kakabisht/Docs-toolkit/internal/crawl"
	"github.com/kakabisht/Docs-toolkit/internal/draft"
	"github.com/kakabisht/Docs-toolkit/internal/gaps"
	"github.com/kakabisht/Docs-toolkit/internal/mcpctx"
	"github.com/spf13/cobra"
)

func newAgentCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agent",
		Short: "Documentation agent: find gaps, fetch context over MCP, draft, validate and evaluate, then wait for approval",
		Long: `The documentation agent works through five steps on a local repository:

  1. gaps      read the code (flags, environment variables, HTTP endpoints,
               configuration keys) and find what the docs miss or describe
               out of date; with --since, what changed since a git ref
  2. context   ask the MCP servers in agent.mcp for product context on each gap
  3. draft     write each page with a citation for every statement (draft write)
  4. validate  check examples (syntax, flags against the code), links,
               citations and facts against the repository
  5. evaluate  score the draft and apply the gate; optionally a model judge

Nothing reaches the docs until a person has verified every claim and run
agent approve. The steps are fixed; a model, when configured in the draft
section of docsbot.yml, writes the prose and can judge it.

  agent gaps      step 1 only: list the gaps
  agent run       all five steps; drafts go to drafts/agent (or --out)
  agent eval      steps 4 and 5 again on an edited draft
  agent approve   record approval; with --publish, write a new page into the docs`,
	}
	c.AddCommand(newAgentGapsCmd(), newAgentRunCmd(), newAgentEvalCmd(), newAgentApproveCmd())
	return c
}

// agentPages are the repository's pages for gap finding, without drafts.
func agentPages(s *crawl.Site) []gaps.Page {
	var out []gaps.Page
	for _, p := range s.Pages {
		if p.RepoPath == "" || p.DocStatus == "draft" || p.FrontMatter["review"] == "required" {
			continue
		}
		out = append(out, gaps.Page{File: p.RepoPath, Title: p.Title, Text: string(p.Raw)})
	}
	return out
}

func localRepo(cmd *cobra.Command, cfg config.Config, source string) (*crawl.Site, error) {
	if isURL(source) {
		return nil, &exitError{code: ExitError, msg: "error: the agent reads a local repository (its code and docs), not a URL"}
	}
	return loadSite(cmd.Context(), cfg, newClient(cfg), source)
}

func newAgentGapsCmd() *cobra.Command {
	var since string
	c := &cobra.Command{
		Use:   "gaps [SOURCE]",
		Short: "List what the code defines that the docs miss or describe out of date",
		Args:  cobra.MaximumNArgs(1),
		Example: `  docsbot agent gaps
  docsbot agent gaps ./my-repo --since origin/main -f json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := textOrJSON(); err != nil {
				return err
			}
			source := firstNonEmpty(firstArg(args), ".")
			cfg, err := loadConfig(source)
			if err != nil {
				return err
			}
			s, err := localRepo(cmd, cfg, source)
			if err != nil {
				return err
			}
			rep, err := gaps.Find(gaps.Options{Root: s.Root, Content: s.Content, Since: since, Pages: agentPages(s)})
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			w, done, err := out()
			if err != nil {
				return err
			}
			defer done()
			if gf.format == "json" {
				if err := writeJSON(w, rep); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(w, "%d surface items in the code, %d documented, %d gaps.\n\n", rep.Items, rep.Covered, len(rep.Gaps))
				for _, g := range rep.Groups {
					fmt.Fprintf(w, "%s  %s  [%s %s, priority %d]\n", g.ID, g.Topic, g.Mode, g.Target, g.Score)
					for _, gap := range g.Gaps {
						line := fmt.Sprintf("      %-12s %s  %s:%d", gap.Type, gap.Item.Name, gap.Item.File, gap.Item.Line)
						if gap.Change != nil {
							line += "  (" + gap.Change.Status
							if gap.Change.OldDefault != "" {
								line += ", was " + gap.Change.OldDefault
							}
							line += " since " + since + ")"
						}
						fmt.Fprintln(w, line)
						for _, m := range gap.Mentions {
							fmt.Fprintf(w, "          still in %s:%d: %s\n", m.File, m.Line, trimTo(m.Text, 80))
						}
					}
				}
				if len(rep.Groups) > 0 {
					fmt.Fprintln(w, "\nNext: docsbot agent run (drafts the top groups) or docsbot agent run --group G1,G2.")
				}
			}
			if len(rep.Gaps) > 0 {
				return &exitError{code: ExitFindings}
			}
			return nil
		},
	}
	c.Flags().StringVar(&since, "since", "", "git ref: also report items changed or removed since it (e.g. origin/main, v1.4.0)")
	return c
}

func trimTo(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func newAgentRunCmd() *cobra.Command {
	var since, outDir, provider, model, endpoint string
	var groups []string
	var max, minScore int
	var allowRemote, judge, force, external, noMCP bool
	c := &cobra.Command{
		Use:   "run [SOURCE]",
		Short: "Find gaps, fetch context over MCP, draft, validate and evaluate",
		Long: `Run all five steps on a local repository (SOURCE, default the current folder).

Each drafted group gets a folder under --out with draft.md, claims.json and
review.md (as draft write), plus agent.json (what it was made from),
eval.json and eval.md (validation and score), and, for pages that exist
already, proposed-changes.md. report.md and run.json summarise the run.

Context servers come from agent.mcp in docsbot.yml, for example:

  agent:
    mcp:
      - name: product-kb
        command: [docsbot, mcp, serve, --no-repo, --kb, ../product-kb]
      - name: api-catalog
        url: https://mcp.example.com/mcp
        allow_remote: true
        header_env: {Authorization: CATALOG_TOKEN}

The model is the draft section's provider; without one the drafts are
extractive. Exit codes: 0 every draft passed the gate, 1 a draft is
blocked or failed, 2 bad input.`,
		Args: cobra.MaximumNArgs(1),
		Example: `  docsbot agent run
  docsbot agent run ./my-repo --since origin/main --max-drafts 5
  docsbot agent run --group G2 --provider anthropic --allow-remote --judge`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := textOrJSON(); err != nil {
				return err
			}
			source := firstNonEmpty(firstArg(args), ".")
			cfg, err := loadConfig(source)
			if err != nil {
				return err
			}
			p, err := newProvider(cfg, provider, model, endpoint, allowRemote)
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			if judge || cfg.Agent.Judge {
				if p == nil {
					return &exitError{code: ExitError, msg: "error: --judge needs a model provider (draft.provider in docsbot.yml, or --provider)"}
				}
			}
			var dopt draft.Options
			s, err := repoSources(cmd, cfg, source, &dopt)
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			var ctxs []*mcpctx.Context
			if !noMCP {
				for _, sc := range cfg.Agent.MCP {
					progress("connecting to MCP server %s", firstNonEmpty(sc.Name, "context"))
					x, err := mcpctx.Connect(cmd.Context(), sc, Version)
					if err != nil {
						return &exitError{code: ExitError, msg: "error: " + err.Error()}
					}
					defer x.Close()
					ctxs = append(ctxs, x)
				}
			}
			opt := agent.Options{
				Root: s.Root, Content: s.Content, Pages: agentPages(s), Docs: dopt.Docs, Evidence: dopt.Evidence, Entities: dopt.Entities,
				Since: since, Contexts: ctxs, Provider: p, Judge: judge || cfg.Agent.Judge,
				Out: firstNonEmpty(outDir, cfg.Agent.Out, filepath.Join("drafts", "agent")), Max: firstInt(max, cfg.Agent.MaxDrafts), Only: groups, Force: force,
				Gate:     agent.Gate{MinScore: firstInt(minScore, cfg.Agent.MinScore), MinJudge: cfg.Agent.MinJudge},
				External: external, Date: time.Now().Format("2006-01-02"), Version: Version,
			}
			if !gf.quiet {
				opt.Progress = func(s string) { fmt.Fprintln(os.Stderr, s) }
			}
			run, err := agent.Execute(cmd.Context(), opt)
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			w, done, err := out()
			if err != nil {
				return err
			}
			defer done()
			if gf.format == "json" {
				if err := writeJSON(w, run); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(w, "%d gaps in %d groups; drafted %d.\n", len(run.Gaps.Gaps), len(run.Gaps.Groups), len(run.Drafts))
				for _, d := range run.Drafts {
					switch {
					case d.Error != "":
						fmt.Fprintf(w, "  %s  error: %s\n", d.Group.ID, d.Error)
					case d.Eval.Gate.Passed:
						fmt.Fprintf(w, "  %s  %s  score %d, gate passed: ready for human review\n", d.Group.ID, d.Dir, d.Eval.Score)
					default:
						fmt.Fprintf(w, "  %s  %s  score %d, blocked: %s\n", d.Group.ID, d.Dir, d.Eval.Score, strings.Join(d.Eval.Gate.Blocking, "; "))
					}
				}
				fmt.Fprintf(w, "Report: %s. Next: review each draft's eval.md and review.md, then docsbot agent approve DIR --by NAME.\n", filepath.Join(opt.Out, agent.RunReport))
			}
			for _, d := range run.Drafts {
				if d.Error != "" || !d.Eval.Gate.Passed {
					return &exitError{code: ExitFindings}
				}
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&since, "since", "", "git ref: also draft fixes for items changed or removed since it")
	f.StringVar(&outDir, "out", "", "run folder (default drafts/agent); never inside the docs")
	f.IntVar(&max, "max-drafts", 0, "groups to draft, highest priority first (default 3)")
	f.StringSliceVar(&groups, "group", nil, "draft these groups (ids from agent gaps), instead of the top --max-drafts")
	f.IntVar(&minScore, "min-score", 0, "evaluation gate, 0–100 (default 80)")
	f.BoolVar(&judge, "judge", false, "also score each draft with the model provider")
	f.BoolVar(&external, "external-links", false, "check external links in the drafts over HTTP")
	f.BoolVar(&noMCP, "no-mcp", false, "don't use the MCP servers in the config")
	f.BoolVar(&force, "force", false, "replace existing drafts and their review records")
	f.StringVar(&provider, "provider", "", "model provider: none, ollama, openai, anthropic (overrides the config)")
	f.StringVar(&model, "model", "", "model name (overrides the config)")
	f.StringVar(&endpoint, "endpoint", "", "provider endpoint (overrides the config)")
	f.BoolVar(&allowRemote, "allow-remote", false, "allow sending source passages to a model endpoint off this machine")
	return c
}

func firstInt(a ...int) int {
	for _, v := range a {
		if v != 0 {
			return v
		}
	}
	return 0
}

func newAgentEvalCmd() *cobra.Command {
	var provider, model, endpoint string
	var allowRemote, judge, external bool
	var minScore int
	c := &cobra.Command{
		Use:   "eval DIR",
		Short: "Validate and score a draft again, after editing it",
		Long: `Run validation (examples, links, citations, facts) and the evaluation gate
again on a draft folder, for example after editing draft.md. Approval needs
an evaluation of the current draft.md, so run this after every edit.`,
		Args:    cobra.ExactArgs(1),
		Example: `  docsbot agent eval drafts/agent/g1-flags-of-the-upgrade-command`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := textOrJSON(); err != nil {
				return err
			}
			dir := args[0]
			meta, err := agent.LoadMeta(dir)
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			cfg, err := loadConfig(meta.Root)
			if err != nil {
				return err
			}
			var p draft.Provider
			if judge || cfg.Agent.Judge {
				if p, err = newProvider(cfg, provider, model, endpoint, allowRemote); err != nil || p == nil {
					return &exitError{code: ExitError, msg: "error: --judge needs a model provider (draft.provider in docsbot.yml, or --provider)"}
				}
			}
			ev, err := draft.LoadEvidence(meta.Root)
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			e, err := agent.Evaluate(cmd.Context(), agent.EvalOptions{Dir: dir, Meta: meta, Evidence: ev, External: external,
				Gate: agent.Gate{MinScore: firstInt(minScore, cfg.Agent.MinScore), MinJudge: cfg.Agent.MinJudge}, Judge: judge || cfg.Agent.Judge,
				Provider: p, Date: time.Now().Format("2006-01-02")})
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			w, done, err := out()
			if err != nil {
				return err
			}
			defer done()
			if gf.format == "json" {
				if err := writeJSON(w, e); err != nil {
					return err
				}
			} else if e.Gate.Passed {
				fmt.Fprintf(w, "Score %d/100: gate passed. Verify the claims in %s, then docsbot agent approve %s --by NAME.\n", e.Score, filepath.Join(dir, draft.ReviewFile), dir)
			} else {
				fmt.Fprintf(w, "Score %d/100: blocked: %s. See %s.\n", e.Score, strings.Join(e.Gate.Blocking, "; "), filepath.Join(dir, agent.EvalReport))
			}
			if !e.Gate.Passed {
				return &exitError{code: ExitFindings}
			}
			return nil
		},
	}
	f := c.Flags()
	f.BoolVar(&judge, "judge", false, "also score the draft with the model provider")
	f.BoolVar(&external, "external-links", false, "check external links over HTTP")
	f.IntVar(&minScore, "min-score", 0, "evaluation gate, 0–100 (default 80)")
	f.StringVar(&provider, "provider", "", "model provider for the judge (overrides the config)")
	f.StringVar(&model, "model", "", "model name (overrides the config)")
	f.StringVar(&endpoint, "endpoint", "", "provider endpoint (overrides the config)")
	f.BoolVar(&allowRemote, "allow-remote", false, "allow sending the draft to a model endpoint off this machine")
	return c
}

func newAgentApproveCmd() *cobra.Command {
	var by string
	var publish, force bool
	c := &cobra.Command{
		Use:   "approve DIR",
		Short: "Record a person's approval of a draft; optionally publish a new page",
		Long: `Approve a draft. This is refused unless:

  - eval.json is an evaluation of the current draft.md (run agent eval after edits),
  - the evaluation gate passed, and
  - every claim in claims.json is verified (docsbot draft verify).

Approval sets status: approved in the draft and writes approval.json. With
--publish, a new page is written into the docs at the target agent gaps gave,
without citations, footnotes or review comments. Existing pages are never
changed: apply proposed-changes.md to them by hand.`,
		Args:    cobra.ExactArgs(1),
		Example: `  docsbot agent approve drafts/agent/g1-flags-of-the-upgrade-command --by "A Reviewer" --publish`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			if by == "" {
				by = os.Getenv("USER")
			}
			a, err := agent.Approve(dir, by, time.Now().Format("2006-01-02"))
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			fmt.Printf("Approved by %s on %s: %d claims verified, score %d.\n", a.By, a.Date, a.Claims, a.Score)
			for _, w := range a.Warnings {
				fmt.Printf("  accepted warning: %s\n", w)
			}
			if !publish {
				meta, _ := agent.LoadMeta(dir)
				if meta.Group.Mode == "update" {
					fmt.Printf("Apply %s to %s by hand.\n", filepath.Join(dir, agent.ChangesFile), meta.Group.Target)
				} else {
					fmt.Printf("Publish with docsbot agent approve %s --publish, or copy the page yourself.\n", dir)
				}
				return nil
			}
			path, err := agent.Publish(dir, force)
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			fmt.Printf("Published %s. Run docsbot check on the repository before you commit it.\n", path)
			return nil
		},
	}
	c.Flags().StringVar(&by, "by", "", "approver's name (default $USER)")
	c.Flags().BoolVar(&publish, "publish", false, "write an approved new page into the docs")
	c.Flags().BoolVar(&force, "force", false, "with --publish, replace a page that exists")
	return c
}
