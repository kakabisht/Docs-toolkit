// Package kg is the knowledge graph of a docs set: pages, sections, entities
// (commands, flags, configuration keys, API endpoints, error messages,
// glossary terms, vocabulary terms), tags, content types, audiences,
// versions, owners and repositories, joined by typed edges. Every edge says
// how it was derived and where: explicit (written by an author: links, front
// matter, navigation), extracted (found by deterministic parsing of the
// content) or inferred (similarity or an optional model, with a confidence).
package kg

import (
	"sort"
	"strings"
)

// Node types.
const (
	TPage     = "page"
	TSection  = "section"
	TEntity   = "entity"
	TTag      = "tag"
	TType     = "content-type"
	TAudience = "audience"
	TVersion  = "version"
	TOwner    = "owner"
	TRepo     = "repository"
	TPartial  = "partial"
	TMissing  = "missing" // target of a broken link
)

// Entity kinds (Node.Kind for entities).
const (
	KCommand  = "command"
	KFlag     = "flag"
	KConfig   = "config-key"
	KAPI      = "api-endpoint"
	KError    = "error-message"
	KGlossary = "glossary-term"
	KTerm     = "term" // from the vocabulary file, or a defined term
)

// Edge types.
const (
	ELinksTo     = "links-to"
	EChildOf     = "child-of"
	EIncludes    = "includes"
	EMentions    = "mentions"
	EDefines     = "defines"
	EDocuments   = "documents"
	ETaggedWith  = "tagged-with"
	EHasType     = "has-type"
	EForAudience = "for-audience"
	EInVersion   = "in-version"
	ESupersedes  = "supersedes"
	EOverlaps    = "overlaps-with"
	ERelated     = "related-to"
	EOwnedBy     = "owned-by"
	EInRepo      = "in-repository"
)

// Derivations.
const (
	Explicit  = "explicit"
	Extracted = "extracted"
	Inferred  = "inferred"
)

// Node is a vertex.
type Node struct {
	ID      string            `json:"id"`
	Type    string            `json:"type"`
	Kind    string            `json:"kind,omitempty"` // entity kind
	Label   string            `json:"label"`
	Aliases []string          `json:"aliases,omitempty"` // other spellings merged into this entity
	Props   map[string]string `json:"props,omitempty"`
}

// Edge is a typed, sourced relation.
type Edge struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	Type       string  `json:"type"`
	Derivation string  `json:"derivation"`
	File       string  `json:"file,omitempty"`
	Line       int     `json:"line,omitempty"`
	Span       string  `json:"span,omitempty"`       // the text the edge was derived from
	Confidence float64 `json:"confidence,omitempty"` // inferred edges only
	Broken     bool    `json:"broken,omitempty"`     // links-to a missing target
}

// Graph is the whole knowledge graph.
type Graph struct {
	Source  string            `json:"source"`
	Entry   string            `json:"entry,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
	Nodes   []*Node           `json:"nodes"`
	Edges   []Edge            `json:"edges"`
	Reviews []Review          `json:"reviews,omitempty"` // suspected duplicates left unmerged

	byID map[string]*Node
}

// Review is something a person should look at instead of the tool merging it.
type Review struct {
	Kind    string   `json:"kind"` // suspected-duplicate-entity
	Nodes   []string `json:"nodes"`
	Message string   `json:"message"`
}

// Node returns the node with id, or nil.
func (g *Graph) Node(id string) *Node {
	if g.byID == nil {
		g.reindex()
	}
	return g.byID[id]
}

func (g *Graph) reindex() {
	g.byID = make(map[string]*Node, len(g.Nodes))
	for _, n := range g.Nodes {
		g.byID[n.ID] = n
	}
}

// add inserts a node once and returns it.
func (g *Graph) add(n *Node) *Node {
	if g.byID == nil {
		g.reindex()
	}
	if old, ok := g.byID[n.ID]; ok {
		return old
	}
	g.byID[n.ID] = n
	g.Nodes = append(g.Nodes, n)
	return n
}

// Normalize sorts nodes and edges and removes duplicate edges, so output is
// stable across runs.
func (g *Graph) Normalize() {
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.SliceStable(g.Edges, func(i, j int) bool {
		a, b := g.Edges[i], g.Edges[j]
		for _, c := range [][2]string{{a.From, b.From}, {a.Type, b.Type}, {a.To, b.To}, {a.File, b.File}} {
			if c[0] != c[1] {
				return c[0] < c[1]
			}
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Span < b.Span
	})
	out := g.Edges[:0]
	for i, e := range g.Edges {
		if i > 0 {
			p := out[len(out)-1]
			if p.From == e.From && p.To == e.To && p.Type == e.Type && p.File == e.File && p.Line == e.Line && p.Span == e.Span {
				continue
			}
		}
		out = append(out, e)
	}
	g.Edges = out
	for _, n := range g.Nodes {
		sort.Strings(n.Aliases)
	}
	g.reindex()
}

// Out returns edges leaving id, optionally filtered by type.
func (g *Graph) Out(id string, types ...string) []Edge {
	var out []Edge
	for _, e := range g.Edges {
		if e.From == id && (len(types) == 0 || in(e.Type, types)) {
			out = append(out, e)
		}
	}
	return out
}

// In returns edges arriving at id, optionally filtered by type.
func (g *Graph) In(id string, types ...string) []Edge {
	var out []Edge
	for _, e := range g.Edges {
		if e.To == id && (len(types) == 0 || in(e.Type, types)) {
			out = append(out, e)
		}
	}
	return out
}

func in(s string, list []string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// PageID and friends build node IDs.
func PageID(key string) string            { return "page:" + key }
func SectionID(key, anchor string) string { return "section:" + key + "#" + anchor }
func EntityID(kind, name string) string   { return "entity:" + kind + ":" + strings.ToLower(name) }
func TagID(t string) string               { return "tag:" + strings.ToLower(t) }
func TypeID(t string) string              { return "content-type:" + t }
func AudienceID(a string) string          { return "audience:" + strings.ToLower(a) }
func VersionID(v string) string           { return "version:" + v }
func OwnerID(o string) string             { return "owner:" + o }
func RepoID(r string) string              { return "repository:" + r }
func PartialID(p string) string           { return "partial:" + p }
func MissingID(target string) string      { return "missing:" + target }
