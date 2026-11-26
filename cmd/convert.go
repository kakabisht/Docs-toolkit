package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kakabisht/Docs-toolkit/internal/check"
	"github.com/kakabisht/Docs-toolkit/internal/convert"
	"github.com/kakabisht/Docs-toolkit/internal/crawl"
	"github.com/kakabisht/Docs-toolkit/internal/finding"
)

func newConvertCmd() *cobra.Command {
	var opt convert.Options
	var outDir string
	var attrs []string
	var dryRun, validate bool
	c := &cobra.Command{
		Use:   "convert SOURCE --out DIR",
		Short: "Convert AsciiDoc to Markdown, with a report of everything approximated, dropped or needing review",
		Long: `Converts AsciiDoc files (a folder, or one file) to Markdown in a separate
output folder; the AsciiDoc is never changed. The converter is native: no
Pandoc needed.

Every construct that has no exact Markdown equivalent is reported with its file
and line: approximated (converted, but looks or behaves differently), dropped
(not in the output), or review (unresolved or raw markup kept). The report is
written to DIR/convert-report.md and DIR/convert-report.json.

  attributes     {name} references are substituted; pass values with --attr
  conditionals   ifdef, ifndef and ifeval are evaluated; every one is reported,
                 and ones that can't be evaluated are kept (or dropped with
                 --unknown drop) and marked for review
  includes       inlined, with tags, lines and leveloffset; files that other
                 files include are not converted on their own
  cross refs     xref: and <<>> point at the target heading's Markdown anchor
  admonitions    by --target: gfm alerts (default), mkdocs, docusaurus, hugo, or
                 commonmark block quotes
  tables         GFM pipe tables, or HTML when cells are merged or hold blocks

Converting the same input twice gives the same output.`,
		Args: cobra.ExactArgs(1),
		Example: `  docsbot convert docs/modules --out markdown
  docsbot convert docs --out md --attr product=Lumen --attr version=2.1 --target mkdocs
  docsbot convert page.adoc --out /tmp/md --dry-run
  docsbot convert docs --out md --check`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := textOrJSON(); err != nil {
				return err
			}
			if outDir == "" {
				return &exitError{code: ExitError, msg: "error: --out is required (the AsciiDoc source is never overwritten)"}
			}
			switch opt.Target {
			case "", convert.TargetGFM, convert.TargetMkDocs, convert.TargetDocusaurus, convert.TargetHugo, convert.TargetPlain:
			default:
				return &exitError{code: ExitError, msg: "error: --target must be gfm, mkdocs, docusaurus, hugo or commonmark"}
			}
			if opt.Unknown != "" && opt.Unknown != "keep" && opt.Unknown != "drop" {
				return &exitError{code: ExitError, msg: "error: --unknown must be keep or drop"}
			}
			opt.Attributes = map[string]string{}
			for _, a := range attrs {
				k, v, _ := strings.Cut(a, "=")
				opt.Attributes[strings.TrimSpace(k)] = v
			}
			conv := convert.New(opt)
			rep, err := conv.Batch(args[0], outDir, dryRun)
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			var fs []finding.Finding
			if validate && !dryRun {
				fs, err = checkConverted(cmd.Context(), outDir)
				if err != nil {
					return err
				}
				if fs == nil {
					fs = []finding.Finding{}
				}
			}
			if !dryRun {
				if err := writeConvertReport(outDir, rep, fs); err != nil {
					return err
				}
			}
			w, done, err := out()
			if err != nil {
				return err
			}
			defer done()
			if gf.format == "json" {
				return writeJSON(w, map[string]any{"report": rep, "link_findings": fs})
			}
			printConvert(w, rep, fs, outDir, dryRun)
			return nil
		},
	}
	c.Flags().StringVar(&outDir, "out", "", "output folder for the Markdown (required; must not be the source folder)")
	c.Flags().StringArrayVarP(&attrs, "attr", "a", nil, "attribute value, name=value (repeatable); overrides the documents")
	c.Flags().StringVar(&opt.Target, "target", "gfm", "Markdown flavour of the target site: gfm, mkdocs, docusaurus, hugo, commonmark")
	c.Flags().StringVar(&opt.AnchorStyle, "anchor-style", "github", "heading anchor style of the target site: github, mkdocs, kramdown, sphinx")
	c.Flags().BoolVar(&opt.H1, "h1", false, "also write the document title as a level-1 heading (it is always in the front matter)")
	c.Flags().StringVar(&opt.Unknown, "unknown", "keep", "conditionals that can't be evaluated: keep or drop (both are reported)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be converted without writing anything")
	c.Flags().BoolVar(&validate, "check", false, "check links and images in the converted Markdown (offline)")
	return c
}

func checkConverted(ctx context.Context, dir string) ([]finding.Finding, error) {
	s, err := crawl.LoadLocal(dir, crawl.LocalOptions{ContentDir: ".", NoGit: true})
	if err != nil {
		return nil, err
	}
	pol := finding.Policy{Only: map[finding.Category]bool{finding.Links: true, finding.Images: true}}
	var keep []finding.Finding
	for _, f := range check.Run(ctx, s, check.Options{Policy: pol, Offline: true}) {
		// the converted folder has no navigation yet, so every page is an orphan
		if f.Rule == "page-orphan" || strings.HasPrefix(f.File, "convert-report.") {
			continue
		}
		keep = append(keep, f)
	}
	return keep, nil
}

func writeConvertReport(out string, rep *convert.Report, fs []finding.Finding) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(out, "convert-report.json"))
	if err != nil {
		return err
	}
	if err := writeJSON(f, map[string]any{"report": rep, "link_findings": fs}); err != nil {
		f.Close()
		return err
	}
	f.Close()
	var links []string
	if fs != nil {
		links = []string{}
		for _, f := range fs {
			links = append(links, fmt.Sprintf("%s:%d %s (%s)", f.File, f.Line, f.Message, f.Rule))
		}
	}
	return os.WriteFile(filepath.Join(out, "convert-report.md"), []byte(rep.Markdown(links)), 0o644)
}

func printConvert(w io.Writer, rep *convert.Report, fs []finding.Finding, out string, dryRun bool) {
	verb := "Converted"
	if dryRun {
		verb = "Would convert"
	}
	n := 0
	for _, f := range rep.Files {
		if f.Skipped == "" {
			n++
		}
	}
	counts := rep.Counts()
	fmt.Fprintf(w, "%s %d files to %s: %d approximated, %d dropped, %d to review.\n", verb, n, out, counts[convert.Approximated], counts[convert.Dropped], counts[convert.Review])
	keys := make([]string, 0, len(rep.Summary))
	for k := range rep.Summary {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if rep.Summary[keys[i]] != rep.Summary[keys[j]] {
			return rep.Summary[keys[i]] > rep.Summary[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > 0 {
		fmt.Fprintln(w, "\nBy construct:")
		for _, k := range keys {
			fmt.Fprintf(w, "  %-40s %d\n", k, rep.Summary[k])
		}
	}
	var review []convert.Item
	for _, it := range rep.Items {
		if it.Action == convert.Review {
			review = append(review, it)
		}
	}
	if len(review) > 0 {
		fmt.Fprintln(w, "\nTo review:")
		for i, it := range review {
			if i == 15 {
				fmt.Fprintf(w, "  … and %d more in convert-report.md\n", len(review)-15)
				break
			}
			fmt.Fprintf(w, "  %s:%d  %s\n", it.File, it.Line, it.Message)
		}
	}
	if len(fs) > 0 {
		fmt.Fprintf(w, "\nLinks and images in the output: %d to fix (see convert-report.md)\n", len(fs))
	}
	if !dryRun {
		fmt.Fprintf(w, "\nFull report: %s\n", filepath.Join(out, "convert-report.md"))
	}
}
