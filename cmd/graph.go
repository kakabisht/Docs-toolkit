package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/docsbot/docsbot/internal/config"
	"github.com/docsbot/docsbot/internal/doctype"
	"github.com/docsbot/docsbot/internal/kg"
	"github.com/docsbot/docsbot/internal/vocab"
)

const defaultGraphDB = "docsbot-graph.db"

func newGraphCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "graph",
		Short: "Build, explore and export the docs knowledge graph",
		Long: `The knowledge graph joins pages, sections, entities (commands, flags,
configuration keys, API endpoints, error messages, glossary and vocabulary
terms), tags, content types, audiences, versions, owners and repositories.

Every edge records how it was derived:
  explicit   written by an author: links, front matter, navigation, includes
  extracted  found by deterministic parsing of the content
  inferred   similarity (related-to, overlaps-with) or a heuristic, with a confidence

and where: the file, line and the text it came from. Broken links point to
"missing" nodes. Suspected duplicate entities are listed for review, never merged.

  docsbot graph build ./docs                 write docsbot-graph.db
  docsbot graph stats                        orphans, hubs, clusters, naming problems
  docsbot graph explain "config file"        what the docs say about one entity, with sources
  docsbot graph view -o graph.html           offline viewer
  docsbot graph view --root install --format mindmap
  docsbot graph export --format graphml -o docs.graphml`,
	}
	c.AddCommand(newGraphBuild(), newGraphStats(), newGraphExplain(), newGraphView(), newGraphExport())
	return c
}

type graphSource struct {
	db string
}

func (gs *graphSource) flags(c *cobra.Command) {
	c.Flags().StringVar(&gs.db, "db", "", "graph file to read or write (default "+defaultGraphDB+", or graph.db in the config)")
}

func (gs *graphSource) path(cfg config.Config) string {
	return firstNonEmpty(gs.db, cfg.Graph.DB, defaultGraphDB)
}

// buildKG reads SOURCE and builds the knowledge graph.
func buildKG(ctx context.Context, cfg config.Config, source string) (*kg.Graph, error) {
	s, err := loadSite(ctx, cfg, newClient(cfg), source)
	if err != nil {
		return nil, err
	}
	v, err := vocab.Load(cfg.Vocabulary)
	if err != nil {
		return nil, fmt.Errorf("vocabulary: %w", err)
	}
	opt := kgOptions(cfg, v)
	if opt.IgnoreHrefs, err = compileAll(cfg.Links.IgnoreHrefs, "links.ignore_hrefs"); err != nil {
		return nil, err
	}
	return kg.Build(s, opt), nil
}

func kgOptions(cfg config.Config, v *vocab.Vocab) kg.Options {
	gc := cfg.Graph
	return kg.Options{
		Vocab: v, ContentTypes: doctype.Overrides(gc.ContentTypes), Commands: gc.Commands,
		NoSimilarity: gc.Similarity != nil && !*gc.Similarity, RelatedThreshold: gc.RelatedThreshold,
		OverlapThreshold: gc.OverlapThreshold, MaxRelated: gc.MaxRelated, Repo: gc.Repository,
	}
}

// openKG builds from SOURCE when given, otherwise loads the graph file.
func (gs *graphSource) open(ctx context.Context, args []string) (*kg.Graph, config.Config, error) {
	cfg, err := loadConfig(firstArg(args))
	if err != nil {
		return nil, cfg, err
	}
	if len(args) > 0 {
		g, err := buildKG(ctx, cfg, args[0])
		return g, cfg, err
	}
	p := gs.path(cfg)
	g, err := kg.Load(p)
	if os.IsNotExist(err) {
		return nil, cfg, &exitError{code: ExitError, msg: fmt.Sprintf("error: no graph at %s: run `docsbot graph build SOURCE` first, or pass SOURCE", p)}
	}
	return g, cfg, err
}

func newGraphBuild() *cobra.Command {
	var gs graphSource
	c := &cobra.Command{
		Use:   "build SOURCE",
		Short: "Read SOURCE and write the knowledge graph to a SQLite file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := textOrJSON(); err != nil {
				return err
			}
			g, cfg, err := gs.open(cmd.Context(), args)
			if err != nil {
				return err
			}
			db := gs.path(cfg)
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
				return writeJSON(w, map[string]any{"db": db, "nodes": st.Nodes, "edges": st.Edges, "derivations": st.Derivations})
			}
			fmt.Fprintf(w, "Wrote %s: %d nodes (%s), %d edges (%s).\n", db, len(g.Nodes), countList(st.Nodes), len(g.Edges), countList(st.Derivations))
			if len(g.Reviews) > 0 {
				fmt.Fprintf(w, "%d suspected duplicate entities to review: docsbot graph stats\n", len(g.Reviews))
			}
			return nil
		},
	}
	gs.flags(c)
	return c
}

func countList(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", m[k], k))
	}
	return strings.Join(parts, ", ")
}

func newGraphStats() *cobra.Command {
	var gs graphSource
	var top int
	c := &cobra.Command{
		Use:   "stats [SOURCE]",
		Short: "Orphans, dead ends, hubs, clusters, depth, broken edges and naming problems",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := textOrJSON(); err != nil {
				return err
			}
			g, _, err := gs.open(cmd.Context(), args)
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
			printKGStats(w, g, st, top)
			return nil
		},
	}
	gs.flags(c)
	c.Flags().IntVar(&top, "top", 10, "items per list")
	return c
}

func printKGStats(w io.Writer, g *kg.Graph, st kg.Stats, top int) {
	label := func(id string) string {
		if n := g.Node(id); n != nil && n.Label != "" && n.Label != strings.TrimPrefix(id, "page:") {
			return fmt.Sprintf("%s (%s)", n.Label, strings.TrimPrefix(id, "page:"))
		}
		return strings.TrimPrefix(id, "page:")
	}
	list := func(title string, ids []string) {
		fmt.Fprintf(w, "\n%s: %d\n", title, len(ids))
		for i, id := range ids {
			if i == top {
				fmt.Fprintf(w, "  … and %d more\n", len(ids)-top)
				break
			}
			fmt.Fprintf(w, "  %s\n", label(id))
		}
	}
	fmt.Fprintf(w, "Knowledge graph of %s\n", g.Source)
	fmt.Fprintf(w, "  Nodes: %s\n", countList(st.Nodes))
	fmt.Fprintf(w, "  Edges: %s\n", countList(st.Edges))
	fmt.Fprintf(w, "  Derivation: %s\n", countList(st.Derivations))
	if len(st.Entities) > 0 {
		fmt.Fprintf(w, "  Entities: %s\n", countList(st.Entities))
	}
	list("Orphan pages (not linked from another page or the navigation)", st.Orphans)
	list("Dead ends (link to no other page)", st.DeadEnds)
	fmt.Fprintln(w, "\nHubs (most links in and out):")
	for _, h := range st.Hubs {
		fmt.Fprintf(w, "  %-50s in %d, out %d\n", label(h.ID), h.In, h.Out)
	}
	fmt.Fprintf(w, "\nClusters: %d connected groups of pages", st.Clusters.Count)
	if len(st.Clusters.Sizes) > 0 {
		sizes := st.Clusters.Sizes
		if len(sizes) > 8 {
			sizes = sizes[:8]
		}
		fmt.Fprintf(w, " (sizes %s)", strings.Trim(fmt.Sprint(sizes), "[]"))
	}
	fmt.Fprintf(w, ", %d isolated pages\n", len(st.Clusters.Isolated))
	if st.Depth.Entry != "" {
		fmt.Fprintf(w, "\nClick depth from %s: max %d", strings.TrimPrefix(st.Depth.Entry, "page:"), st.Depth.Max)
		var ds []string
		for d := 0; d <= st.Depth.Max; d++ {
			ds = append(ds, fmt.Sprintf("%d: %d", d, st.Depth.Histogram[d]))
		}
		fmt.Fprintf(w, " (%s), %d unreachable\n", strings.Join(ds, ", "), len(st.Depth.Unreachable))
	}
	fmt.Fprintf(w, "\nBroken edges: %d\n", len(st.Broken))
	for i, e := range st.Broken {
		if i == top {
			fmt.Fprintf(w, "  … and %d more (docsbot check lists them all)\n", len(st.Broken)-top)
			break
		}
		fmt.Fprintf(w, "  %s:%d -> %s\n", e.File, e.Line, strings.TrimPrefix(e.To, "missing:"))
	}
	uses := func(title string, es []kg.EntityUse) {
		fmt.Fprintf(w, "\n%s: %d\n", title, len(es))
		for i, e := range es {
			if i == top {
				fmt.Fprintf(w, "  … and %d more\n", len(es)-top)
				break
			}
			fmt.Fprintf(w, "  %s (%d pages)\n", e.Label, len(e.Pages))
		}
	}
	uses("Entities mentioned but defined or documented nowhere", st.Undefined)
	uses("Entities defined on more than one page", st.MultiDefined)
	fmt.Fprintf(w, "\nInconsistent names: %d\n", len(st.Inconsistent))
	for i, n := range st.Inconsistent {
		if i == top {
			fmt.Fprintf(w, "  … and %d more\n", len(st.Inconsistent)-top)
			break
		}
		var sp []string
		for s, c := range n.Spellings {
			sp = append(sp, fmt.Sprintf("%q ×%d", s, c))
		}
		sort.Strings(sp)
		fmt.Fprintf(w, "  %s: %s\n", n.Label, strings.Join(sp, ", "))
	}
	if len(st.Reviews) > 0 {
		fmt.Fprintf(w, "\nFor review (not merged): %d\n", len(st.Reviews))
		for i, r := range st.Reviews {
			if i == top {
				fmt.Fprintf(w, "  … and %d more\n", len(st.Reviews)-top)
				break
			}
			fmt.Fprintf(w, "  %s\n", r.Message)
		}
	}
}

func newGraphExplain() *cobra.Command {
	var gs graphSource
	var limit int
	c := &cobra.Command{
		Use:   "explain ENTITY",
		Short: "Show what the docs say about one entity, with file and line for each claim",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := textOrJSON(); err != nil {
				return err
			}
			g, _, err := gs.open(cmd.Context(), nil)
			if err != nil {
				return err
			}
			x, err := g.Explain(args[0])
			if err != nil {
				return &exitError{code: ExitError, msg: "error: " + err.Error()}
			}
			w, done, err := out()
			if err != nil {
				return err
			}
			defer done()
			if gf.format == "json" {
				return writeJSON(w, x)
			}
			x.WriteText(w, g, limit)
			return nil
		},
	}
	gs.flags(c)
	c.Flags().IntVar(&limit, "limit", 10, "sources per list in text output")
	return c
}

type filterFlags struct {
	f     kg.Filter
	types []string
}

func (ff *filterFlags) add(c *cobra.Command, defMax int) {
	fl := c.Flags()
	fl.StringSliceVar(&ff.types, "type", nil, "node types or entity kinds to keep: page, section, entity, tag, content-type, audience, version, owner, repository, partial, missing, command, config-key, …")
	fl.StringVar(&ff.f.Section, "section", "", "pages under this path (e.g. guide/) and what they touch")
	fl.StringVar(&ff.f.Tag, "tag", "", "pages with this tag and what they touch")
	fl.StringVar(&ff.f.Entity, "entity", "", "this entity and the pages around it")
	fl.StringVar(&ff.f.Root, "root", "", "start from this node: an entity, page, tag or ID")
	fl.IntVar(&ff.f.Depth, "depth", 0, "hops from --root, --entity, --tag or --section (default 1, or 2 with --root)")
	fl.IntVar(&ff.f.MaxNodes, "max-nodes", defMax, "keep the most connected nodes (0 for no limit)")
	fl.BoolVar(&ff.f.Inferred, "inferred", true, "include inferred related-to and overlaps-with edges")
}

func (ff *filterFlags) apply(g *kg.Graph) (*kg.Graph, error) {
	ff.f.Types = ff.types
	if ff.f.Empty() && ff.f.Inferred {
		return g, nil
	}
	sg, err := g.Subgraph(ff.f)
	if err != nil {
		return nil, &exitError{code: ExitError, msg: "error: " + err.Error()}
	}
	return sg, nil
}

func newGraphView() *cobra.Command {
	var gs graphSource
	var ff filterFlags
	c := &cobra.Command{
		Use:   "view [SOURCE]",
		Short: "Draw the graph: offline HTML viewer, Mermaid, DOT, or a mind map around --root",
		Long: `Formats (--format):
  html      self-contained viewer with search, type filters and a detail panel (default)
  mermaid   Mermaid flowchart
  dot       Graphviz
  mindmap   Mermaid mind map around --root
  outline   the same tree as a Markdown outline`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format := gf.format
			if format == "text" {
				format = "html"
			}
			switch format {
			case "html", "mermaid", "dot", "mindmap", "outline":
			default:
				return &exitError{code: ExitError, msg: "error: graph view supports --format html, mermaid, dot, mindmap or outline"}
			}
			if (format == "mindmap" || format == "outline") && ff.f.Root == "" {
				return &exitError{code: ExitError, msg: "error: --format " + format + " needs --root"}
			}
			g, _, err := gs.open(cmd.Context(), args)
			if err != nil {
				return err
			}
			if format == "mindmap" || format == "outline" {
				// the tree is cut by depth, not by the subgraph
				root := ff.f.Root
				ff.f.Root, ff.f.MaxNodes = "", 0
				if g, err = ff.apply(g); err != nil {
					return err
				}
				w, done, err := out()
				if err != nil {
					return err
				}
				defer done()
				if format == "mindmap" {
					err = g.Mindmap(w, root, ff.f.Depth)
				} else {
					err = g.Outline(w, root, ff.f.Depth)
				}
				if err != nil {
					return &exitError{code: ExitError, msg: "error: " + err.Error()}
				}
				return nil
			}
			if g, err = ff.apply(g); err != nil {
				return err
			}
			w, done, err := out()
			if err != nil {
				return err
			}
			defer done()
			switch format {
			case "mermaid":
				return g.Mermaid(w)
			case "dot":
				return g.DOT(w)
			}
			if err := g.HTML(w); err != nil {
				return err
			}
			if gf.output != "" {
				progress("wrote %s (%d nodes, %d edges); open it in a browser, no network needed", gf.output, len(g.Nodes), len(g.Edges))
			}
			return nil
		},
	}
	gs.flags(c)
	ff.add(c, 2000)
	return c
}

func newGraphExport() *cobra.Command {
	var gs graphSource
	var ff filterFlags
	c := &cobra.Command{
		Use:   "export [SOURCE]",
		Short: "Export the graph as JSON, GraphML, CSV, JSON-LD or Cypher",
		Long: `Formats (--format):
  json      nodes, edges and reviews (default)
  graphml   for Gephi, yEd, Cytoscape and NetworkX
  csv       two files: PREFIX.nodes.csv and PREFIX.edges.csv (PREFIX from -o, default "graph")
  jsonld    JSON-LD, with each edge reified so its provenance survives
  cypher    statements for Neo4j or Memgraph
  mermaid, dot  as graph view`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format := gf.format
			if format == "text" {
				format = "json"
			}
			g, _, err := gs.open(cmd.Context(), args)
			if err != nil {
				return err
			}
			if g, err = ff.apply(g); err != nil {
				return err
			}
			if format == "csv" {
				prefix := strings.TrimSuffix(firstNonEmpty(gf.output, "graph"), ".csv")
				nf, err := os.Create(prefix + ".nodes.csv")
				if err != nil {
					return err
				}
				defer nf.Close()
				ef, err := os.Create(prefix + ".edges.csv")
				if err != nil {
					return err
				}
				defer ef.Close()
				if err := g.CSV(nf, ef); err != nil {
					return err
				}
				progress("wrote %s.nodes.csv and %s.edges.csv", prefix, prefix)
				return nil
			}
			w, done, err := out()
			if err != nil {
				return err
			}
			defer done()
			switch format {
			case "json":
				return g.JSON(w)
			case "graphml":
				return g.GraphML(w)
			case "jsonld", "json-ld":
				return g.JSONLD(w)
			case "cypher":
				return g.Cypher(w)
			case "mermaid":
				return g.Mermaid(w)
			case "dot":
				return g.DOT(w)
			}
			return &exitError{code: ExitError, msg: "error: graph export supports --format json, graphml, csv, jsonld, cypher, mermaid or dot"}
		},
	}
	gs.flags(c)
	ff.add(c, 0)
	return c
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
