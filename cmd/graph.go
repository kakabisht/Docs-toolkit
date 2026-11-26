package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/docsbot/docsbot/internal/graph"
)

const defaultDB = "docsbot.db"

func newGraphCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "graph",
		Short: "Build, inspect and export the docs knowledge graph",
		Long: `The knowledge graph has pages and sections as nodes, and "contains" and
"links_to" edges. It is stored in one SQLite file (default docsbot.db) with
tables pages, sections, anchors, links, code_blocks and views nodes, edges.`,
	}
	c.AddCommand(newGraphBuild(), newGraphStats(), newGraphExport())
	return c
}

func newGraphBuild() *cobra.Command {
	var db string
	c := &cobra.Command{
		Use:   "build SOURCE",
		Short: "Crawl SOURCE and write the graph to a SQLite file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			g, err := loadGraph(cmd.Context(), cfg, newClient(cfg), args, "")
			if err != nil {
				return err
			}
			if err := g.Save(db); err != nil {
				return fmt.Errorf("save %s: %w", db, err)
			}
			st := g.ComputeStats(5)
			w, done, err := out()
			if err != nil {
				return err
			}
			defer done()
			if gf.format == "json" {
				return writeJSON(w, map[string]any{"db": db, "stats": st})
			}
			fmt.Fprintf(w, "Wrote %s: %d pages, %d sections, %d links (%d between pages).\n", db, st.Pages, st.Sections, st.Links, st.PageEdges)
			fmt.Fprintln(w, "Next: docsbot graph stats --db "+db+"   or   docsbot graph export --db "+db+" --as html -o graph.html")
			return nil
		},
	}
	c.Flags().StringVar(&db, "db", defaultDB, "SQLite file to write")
	return c
}

func newGraphStats() *cobra.Command {
	var db string
	var top int
	c := &cobra.Command{
		Use:   "stats [SOURCE]",
		Short: "Summarize the graph: size, orphans, depth, most important pages",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if len(args) == 0 && db == "" {
				db = defaultDB
			}
			g, err := loadGraph(cmd.Context(), cfg, newClient(cfg), args, db)
			if err != nil {
				return err
			}
			st := g.ComputeStats(top)
			w, done, err := out()
			if err != nil {
				return err
			}
			defer done()
			if gf.format == "json" {
				return writeJSON(w, st)
			}
			printStats(w, g, st)
			return nil
		},
	}
	c.Flags().StringVar(&db, "db", "", "read a graph built with `graph build` (default docsbot.db when no SOURCE)")
	c.Flags().IntVar(&top, "top", 10, "length of ranked lists")
	return c
}

func printStats(w io.Writer, g *graph.Graph, st graph.Stats) {
	fmt.Fprintf(w, "Knowledge graph for %s\n\n", g.Source)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  Pages\t%d\n", st.Pages)
	if st.ErrorPages > 0 {
		fmt.Fprintf(tw, "  Linked URLs that failed\t%d (see `docsbot links`)\n", st.ErrorPages)
	}
	fmt.Fprintf(tw, "  Sections\t%d\n  Words\t%d\n  Code blocks\t%d\n", st.Sections, st.Words, st.CodeBlocks)
	fmt.Fprintf(tw, "  Links\t%d (internal %d, anchor %d, asset %d, external %d)\n", st.Links, st.InternalLinks, st.AnchorLinks, st.AssetLinks, st.ExternalLinks)
	fmt.Fprintf(tw, "  Page-to-page edges\t%d\n  Entry page\t%s\n  Max click depth\t%d\n", st.PageEdges, st.Entry, st.MaxDepth)
	fmt.Fprintf(tw, "  Orphan pages\t%d\n  Unreachable from entry\t%d\n  Dead ends (no outbound)\t%d\n", len(st.Orphans), len(st.Unreachable), len(st.DeadEnds))
	tw.Flush()

	fmt.Fprintln(w, "\nMost important pages (PageRank)")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  RANK\tIN\tOUT\tPAGE")
	for _, p := range st.Top {
		fmt.Fprintf(tw, "  %.3f\t%d\t%d\t%s\n", p.Rank, p.In, p.Out, pageLabel(p.Key, p.Title))
	}
	tw.Flush()

	if len(st.Orphans) > 0 {
		fmt.Fprintln(w, "\nOrphan pages (nothing links to them)")
		for i, k := range st.Orphans {
			if i == 10 {
				fmt.Fprintf(w, "  … and %d more (use --format json for all)\n", len(st.Orphans)-10)
				break
			}
			fmt.Fprintln(w, "  "+k)
		}
	}
	if len(st.ExternalHosts) > 0 {
		fmt.Fprintln(w, "\nMost linked external hosts")
		for _, h := range st.ExternalHosts {
			fmt.Fprintf(w, "  %5d  %s\n", h.Links, h.Host)
		}
	}
}

func pageLabel(key, title string) string {
	if title == "" {
		return key
	}
	return fmt.Sprintf("%s  (%s)", title, key)
}

func newGraphExport() *cobra.Command {
	var db, as, title string
	var maxNodes int
	var sections bool
	c := &cobra.Command{
		Use:   "export [SOURCE]",
		Short: "Export the graph as Mermaid, DOT, JSON, an HTML viewer, or a generated llms.txt",
		Args:  cobra.MaximumNArgs(1),
		Example: `  docsbot graph export --db docsbot.db --as html -o graph.html
  docsbot graph export ./docs --as mermaid --max-nodes 40
  docsbot graph export https://docs.example.com --as llms-txt -o llms.txt`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if len(args) == 0 && db == "" {
				db = defaultDB
			}
			g, err := loadGraph(cmd.Context(), cfg, newClient(cfg), args, db)
			if err != nil {
				return err
			}
			w, done, err := out()
			if err != nil {
				return err
			}
			defer done()
			opt := graph.ExportOptions{MaxNodes: maxNodes, Sections: sections}
			switch strings.ToLower(as) {
			case "mermaid", "mmd":
				if maxNodes == 0 {
					opt.MaxNodes = 60 // Mermaid becomes unreadable past this
				}
				return g.Mermaid(w, opt)
			case "dot", "graphviz":
				return g.DOT(w, opt)
			case "json":
				return g.JSON(w, opt)
			case "html":
				return g.HTML(w, opt)
			case "llms-txt", "llms.txt", "llms":
				return g.WriteLLMSTxt(w, title)
			}
			return fmt.Errorf("unknown --as %q (mermaid, dot, json, html, llms-txt)", as)
		},
	}
	c.Flags().StringVar(&db, "db", "", "read a graph built with `graph build` (default docsbot.db when no SOURCE)")
	c.Flags().StringVar(&as, "as", "html", "export format: mermaid, dot, json, html, llms-txt")
	c.Flags().IntVar(&maxNodes, "max-nodes", 0, "keep only the top N pages by PageRank (mermaid defaults to 60)")
	c.Flags().BoolVar(&sections, "sections", false, "include section nodes (mermaid, dot, json)")
	c.Flags().StringVar(&title, "title", "", "llms-txt: site title (default: entry page title)")
	return c
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
