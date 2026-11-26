package kg

import (
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
)

// JSON writes the graph as indented JSON (the golden format).
func (g *Graph) JSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(g)
}

// CSV writes nodes and edges as two CSV tables.
func (g *Graph) CSV(nodes, edges io.Writer) error {
	nw := csv.NewWriter(nodes)
	nw.Write([]string{"id", "type", "kind", "label", "aliases", "props"})
	for _, n := range g.Nodes {
		nw.Write([]string{n.ID, n.Type, n.Kind, n.Label, strings.Join(n.Aliases, "|"), propString(n.Props)})
	}
	nw.Flush()
	if err := nw.Error(); err != nil {
		return err
	}
	ew := csv.NewWriter(edges)
	ew.Write([]string{"from", "to", "type", "derivation", "file", "line", "span", "confidence", "broken"})
	for _, e := range g.Edges {
		ew.Write([]string{e.From, e.To, e.Type, e.Derivation, e.File, itoa(e.Line), e.Span, ftoa(e.Confidence), fmt.Sprint(e.Broken)})
	}
	ew.Flush()
	return ew.Error()
}

func itoa(i int) string {
	if i == 0 {
		return ""
	}
	return fmt.Sprint(i)
}

func ftoa(f float64) string {
	if f == 0 {
		return ""
	}
	return fmt.Sprintf("%.2f", f)
}

func propString(p map[string]string) string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+p[k])
	}
	return strings.Join(parts, "; ")
}

// GraphML writes the graph for Gephi, yEd, Cytoscape and NetworkX.
func (g *Graph) GraphML(w io.Writer) error {
	type data struct {
		Key   string `xml:"key,attr"`
		Value string `xml:",chardata"`
	}
	type node struct {
		ID   string `xml:"id,attr"`
		Data []data `xml:"data"`
	}
	type edge struct {
		Source string `xml:"source,attr"`
		Target string `xml:"target,attr"`
		Data   []data `xml:"data"`
	}
	type key struct {
		ID   string `xml:"id,attr"`
		For  string `xml:"for,attr"`
		Name string `xml:"attr.name,attr"`
		Type string `xml:"attr.type,attr"`
	}
	type graph struct {
		ID          string `xml:"id,attr"`
		EdgeDefault string `xml:"edgedefault,attr"`
		Nodes       []node `xml:"node"`
		Edges       []edge `xml:"edge"`
	}
	type doc struct {
		XMLName xml.Name `xml:"graphml"`
		NS      string   `xml:"xmlns,attr"`
		Keys    []key    `xml:"key"`
		Graph   graph    `xml:"graph"`
	}
	d := doc{NS: "http://graphml.graphdrawing.org/xmlns", Graph: graph{ID: "docs", EdgeDefault: "directed"}}
	for _, k := range []key{
		{"type", "node", "type", "string"}, {"kind", "node", "kind", "string"}, {"label", "node", "label", "string"},
		{"aliases", "node", "aliases", "string"}, {"props", "node", "props", "string"},
		{"etype", "edge", "type", "string"}, {"derivation", "edge", "derivation", "string"}, {"file", "edge", "file", "string"},
		{"line", "edge", "line", "int"}, {"span", "edge", "span", "string"}, {"confidence", "edge", "confidence", "double"},
		{"broken", "edge", "broken", "boolean"},
	} {
		d.Keys = append(d.Keys, k)
	}
	opt := func(ds []data, k, v string) []data {
		if v != "" {
			ds = append(ds, data{k, v})
		}
		return ds
	}
	for _, n := range g.Nodes {
		ds := []data{{"type", n.Type}}
		ds = opt(ds, "kind", n.Kind)
		ds = opt(ds, "label", n.Label)
		ds = opt(ds, "aliases", strings.Join(n.Aliases, "|"))
		ds = opt(ds, "props", propString(n.Props))
		d.Graph.Nodes = append(d.Graph.Nodes, node{n.ID, ds})
	}
	for _, e := range g.Edges {
		ds := []data{{"etype", e.Type}, {"derivation", e.Derivation}}
		ds = opt(ds, "file", e.File)
		ds = opt(ds, "line", itoa(e.Line))
		ds = opt(ds, "span", e.Span)
		ds = opt(ds, "confidence", ftoa(e.Confidence))
		if e.Broken {
			ds = append(ds, data{"broken", "true"})
		}
		d.Graph.Edges = append(d.Graph.Edges, edge{e.From, e.To, ds})
	}
	io.WriteString(w, xml.Header)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(d); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// NS is the namespace used for JSON-LD terms. It is a placeholder until the
// project has a home page.
const NS = "urn:docsbot:"

// JSONLD writes the graph as JSON-LD: nodes with their outgoing relations,
// and every edge reified with its provenance.
func (g *Graph) JSONLD(w io.Writer) error {
	iri := func(id string) string { return NS + "node:" + url.PathEscape(id) }
	ctx := map[string]any{
		"@vocab":     NS + "vocab#",
		"label":      "http://www.w3.org/2000/01/rdf-schema#label",
		"aliases":    "http://www.w3.org/2004/02/skos/core#altLabel",
		"from":       map[string]string{"@type": "@id"},
		"to":         map[string]string{"@type": "@id"},
		"confidence": map[string]string{"@type": "http://www.w3.org/2001/XMLSchema#decimal"},
	}
	var items []map[string]any
	out := map[string]map[string][]map[string]string{}
	for _, e := range g.Edges {
		if out[e.From] == nil {
			out[e.From] = map[string][]map[string]string{}
		}
		out[e.From][e.Type] = append(out[e.From][e.Type], map[string]string{"@id": iri(e.To)})
	}
	for _, n := range g.Nodes {
		m := map[string]any{"@id": iri(n.ID), "@type": n.Type, "label": n.Label}
		if n.Kind != "" {
			m["kind"] = n.Kind
		}
		if len(n.Aliases) > 0 {
			m["aliases"] = n.Aliases
		}
		for k, v := range n.Props {
			m[k] = v
		}
		for t, ts := range out[n.ID] {
			m[t] = ts
		}
		items = append(items, m)
	}
	for i, e := range g.Edges {
		m := map[string]any{"@id": fmt.Sprintf("%sedge:%d", NS, i), "@type": "Edge", "relation": e.Type,
			"from": iri(e.From), "to": iri(e.To), "derivation": e.Derivation}
		if e.File != "" {
			m["file"] = e.File
		}
		if e.Line > 0 {
			m["line"] = e.Line
		}
		if e.Span != "" {
			m["span"] = e.Span
		}
		if e.Confidence > 0 {
			m["confidence"] = e.Confidence
		}
		if e.Broken {
			m["broken"] = true
		}
		items = append(items, m)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(map[string]any{"@context": ctx, "@graph": items})
}

var cypherLabel = map[string]string{
	TPage: "Page", TSection: "Section", TEntity: "Entity", TTag: "Tag", TType: "ContentType",
	TAudience: "Audience", TVersion: "Version", TOwner: "Owner", TRepo: "Repository", TPartial: "Partial", TMissing: "Missing",
}

func cypherString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\n`, "\r", "")
	return "'" + r.Replace(s) + "'"
}

// Cypher writes statements that load the graph into Neo4j or Memgraph.
func (g *Graph) Cypher(w io.Writer) error {
	fmt.Fprintf(w, "// Knowledge graph of %s\n", g.Source)
	fmt.Fprintln(w, "CREATE CONSTRAINT doc_node_id IF NOT EXISTS FOR (n:DocNode) REQUIRE n.id IS UNIQUE;")
	for _, n := range g.Nodes {
		label := cypherLabel[n.Type]
		if label == "" {
			label = "Other"
		}
		props := []string{"id: " + cypherString(n.ID), "label: " + cypherString(n.Label)}
		if n.Kind != "" {
			props = append(props, "kind: "+cypherString(n.Kind))
		}
		if len(n.Aliases) > 0 {
			var as []string
			for _, a := range n.Aliases {
				as = append(as, cypherString(a))
			}
			props = append(props, "aliases: ["+strings.Join(as, ", ")+"]")
		}
		fmt.Fprintf(w, "MERGE (:DocNode:%s {%s});\n", label, strings.Join(props, ", "))
	}
	for _, e := range g.Edges {
		rel := strings.ToUpper(strings.ReplaceAll(e.Type, "-", "_"))
		props := []string{"derivation: " + cypherString(e.Derivation)}
		if e.File != "" {
			props = append(props, "file: "+cypherString(e.File))
		}
		if e.Line > 0 {
			props = append(props, fmt.Sprintf("line: %d", e.Line))
		}
		if e.Span != "" {
			props = append(props, "span: "+cypherString(e.Span))
		}
		if e.Confidence > 0 {
			props = append(props, fmt.Sprintf("confidence: %.2f", e.Confidence))
		}
		if e.Broken {
			props = append(props, "broken: true")
		}
		fmt.Fprintf(w, "MATCH (a:DocNode {id: %s}), (b:DocNode {id: %s}) CREATE (a)-[:%s {%s}]->(b);\n",
			cypherString(e.From), cypherString(e.To), rel, strings.Join(props, ", "))
	}
	return nil
}

// Mermaid writes a flowchart. Broken links and inferred edges are dashed.
func (g *Graph) Mermaid(w io.Writer) error {
	ids := shortIDs(g)
	fmt.Fprintln(w, "flowchart LR")
	for _, n := range g.Nodes {
		l := mermaidText(nodeLabel(n))
		var shape string
		switch n.Type {
		case TPage:
			shape = fmt.Sprintf("[\"%s\"]", l)
		case TSection:
			shape = fmt.Sprintf("(\"%s\")", l)
		case TEntity:
			shape = fmt.Sprintf("{{\"%s\"}}", l)
		case TMissing:
			shape = fmt.Sprintf("[/\"missing: %s\"/]", l)
		default:
			shape = fmt.Sprintf("([\"%s\"])", l)
		}
		fmt.Fprintf(w, "  %s%s:::%s\n", ids[n.ID], shape, strings.ReplaceAll(n.Type, "-", "_"))
	}
	for _, e := range g.Edges {
		arrow := "-->"
		if e.Broken || e.Derivation == Inferred {
			arrow = "-.->"
		}
		fmt.Fprintf(w, "  %s %s|%s| %s\n", ids[e.From], arrow, e.Type, ids[e.To])
	}
	fmt.Fprintln(w, "  classDef missing stroke:#c62828,stroke-dasharray:4 3,color:#c62828")
	fmt.Fprintln(w, "  classDef entity fill:#eef4ff,stroke:#3b6fd8")
	fmt.Fprintln(w, "  classDef tag fill:#f3f8ee,stroke:#5b8c2a")
	return nil
}

func mermaidText(s string) string {
	return strings.NewReplacer(`"`, "#quot;", "\n", " ", "<", "#lt;", ">", "#gt;").Replace(s)
}

func nodeLabel(n *Node) string {
	if n.Label != "" {
		return n.Label
	}
	return n.ID
}

func shortIDs(g *Graph) map[string]string {
	m := map[string]string{}
	for i, n := range g.Nodes {
		m[n.ID] = fmt.Sprintf("n%d", i)
	}
	// edges to nodes outside the subgraph never appear, but be safe
	for _, e := range g.Edges {
		for _, id := range []string{e.From, e.To} {
			if _, ok := m[id]; !ok {
				m[id] = fmt.Sprintf("x%d", len(m))
			}
		}
	}
	return m
}

var dotColour = map[string]string{
	TPage: "#29335c", TSection: "#669bbc", TEntity: "#3b6fd8", TTag: "#5b8c2a", TType: "#8e7dbe",
	TAudience: "#f3a712", TVersion: "#a8c686", TOwner: "#e4572e", TRepo: "#555555", TPartial: "#999999", TMissing: "#c62828",
}

// DOT writes Graphviz. Broken links and inferred edges are dashed.
func (g *Graph) DOT(w io.Writer) error {
	fmt.Fprintln(w, "digraph docs {")
	fmt.Fprintln(w, `  graph [rankdir=LR, overlap=false, splines=true];`)
	fmt.Fprintln(w, `  node [fontname="Helvetica", fontsize=10, style=rounded, shape=box];`)
	fmt.Fprintln(w, `  edge [fontname="Helvetica", fontsize=8, color="#888888"];`)
	for _, n := range g.Nodes {
		shape := "box"
		switch n.Type {
		case TEntity:
			shape = "hexagon"
		case TSection:
			shape = "note"
		case TMissing:
			shape = "octagon"
		case TTag, TType, TAudience, TVersion, TOwner, TRepo:
			shape = "ellipse"
		}
		style := "rounded"
		if n.Type == TMissing {
			style = "dashed"
		}
		fmt.Fprintf(w, "  %q [label=%q, shape=%s, style=%q, color=%q, tooltip=%q];\n", n.ID, nodeLabel(n), shape, style, dotColour[n.Type], n.ID)
	}
	for _, e := range g.Edges {
		attrs := fmt.Sprintf("label=%q", e.Type)
		switch {
		case e.Broken:
			attrs += `, style=dashed, color="#c62828"`
		case e.Derivation == Inferred:
			attrs += `, style=dashed`
		}
		fmt.Fprintf(w, "  %q -> %q [%s];\n", e.From, e.To, attrs)
	}
	fmt.Fprintln(w, "}")
	return nil
}

var inverseNames = map[string]string{
	ELinksTo: "linked from", EChildOf: "children", EIncludes: "included by", EMentions: "mentioned by",
	EDefines: "defined by", EDocuments: "documented by", ETaggedWith: "pages", EHasType: "pages",
	EForAudience: "pages", EInVersion: "pages", ESupersedes: "superseded by", EOverlaps: "overlaps-with",
	ERelated: "related-to", EOwnedBy: "owns", EInRepo: "pages",
}

func inverse(t string) string {
	if n, ok := inverseNames[t]; ok {
		return n
	}
	return t + " (inverse)"
}

// tree is a spanning tree from a root, for mind maps and outlines.
type tree struct {
	node     *Node
	via      string
	children []*tree
}

func (g *Graph) spanningTree(root string, depth int) *tree {
	adj := map[string][]Edge{}
	for _, e := range g.Edges {
		adj[e.From] = append(adj[e.From], e)
		adj[e.To] = append(adj[e.To], Edge{From: e.To, To: e.From, Type: inverse(e.Type)})
	}
	for k := range adj {
		sort.SliceStable(adj[k], func(i, j int) bool {
			a, b := adj[k][i], adj[k][j]
			if a.Type != b.Type {
				return a.Type < b.Type
			}
			return a.To < b.To
		})
	}
	seen := map[string]bool{root: true}
	t := &tree{node: g.Node(root)}
	level := []*tree{t}
	for d := 0; d < depth && len(level) > 0; d++ {
		var next []*tree
		for _, p := range level {
			for _, e := range adj[p.node.ID] {
				if seen[e.To] || g.Node(e.To) == nil {
					continue
				}
				seen[e.To] = true
				c := &tree{node: g.Node(e.To), via: e.Type}
				p.children = append(p.children, c)
				next = append(next, c)
			}
		}
		level = next
	}
	return t
}

// Mindmap writes a Mermaid mind map around root.
func (g *Graph) Mindmap(w io.Writer, root string, depth int) error {
	n, _ := g.Find(root)
	if n == nil {
		return fmt.Errorf("no node matches %q", root)
	}
	if depth == 0 {
		depth = 2
	}
	t := g.spanningTree(n.ID, depth)
	fmt.Fprintln(w, "mindmap")
	var walk func(t *tree, indent int)
	walk = func(t *tree, indent int) {
		label := strings.NewReplacer("(", "", ")", "", "[", "", "]", "", "{", "", "}", "").Replace(nodeLabel(t.node))
		pad := strings.Repeat("  ", indent)
		if indent == 1 {
			fmt.Fprintf(w, "%sroot((%s))\n", pad, label)
		} else {
			fmt.Fprintf(w, "%s%s\n", pad, label)
		}
		// group children by relation so the map reads as categories
		groups := map[string][]*tree{}
		var order []string
		for _, c := range t.children {
			if _, ok := groups[c.via]; !ok {
				order = append(order, c.via)
			}
			groups[c.via] = append(groups[c.via], c)
		}
		for _, via := range order {
			fmt.Fprintf(w, "%s  [%s]\n", pad, via)
			for _, c := range groups[via] {
				walk(c, indent+2)
			}
		}
	}
	walk(t, 1)
	return nil
}

// Outline writes the same tree as a Markdown outline.
func (g *Graph) Outline(w io.Writer, root string, depth int) error {
	n, _ := g.Find(root)
	if n == nil {
		return fmt.Errorf("no node matches %q", root)
	}
	if depth == 0 {
		depth = 2
	}
	t := g.spanningTree(n.ID, depth)
	fmt.Fprintf(w, "# %s\n\n", nodeLabel(n))
	var walk func(t *tree, indent int)
	walk = func(t *tree, indent int) {
		for _, c := range t.children {
			fmt.Fprintf(w, "%s- %s _(%s, %s)_\n", strings.Repeat("  ", indent), nodeLabel(c.node), c.node.Type, c.via)
			walk(c, indent+1)
		}
	}
	walk(t, 0)
	return nil
}
