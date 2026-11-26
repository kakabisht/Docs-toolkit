package kg

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Explanation is everything the graph knows about one entity, with sources.
type Explanation struct {
	Node         *Node          `json:"node"`
	Defines      []Edge         `json:"defined_by"`
	Documents    []Edge         `json:"documented_by"`
	Mentions     []Edge         `json:"mentioned_by"`
	MentionPages int            `json:"mention_pages"`
	Spellings    map[string]int `json:"spellings,omitempty"`
	CoMentioned  []Related      `json:"often_mentioned_with,omitempty"`
	Tags         []Related      `json:"tags_of_pages,omitempty"`
	Reviews      []Review       `json:"reviews,omitempty"`
	Others       []string       `json:"other_matches,omitempty"`
}

// Related is a node with a count.
type Related struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Explain gathers what the graph says about q (an entity, or any node).
func (g *Graph) Explain(q string) (*Explanation, error) {
	n, others := g.Find(q)
	if n == nil {
		return nil, fmt.Errorf("nothing in the graph matches %q", q)
	}
	x := &Explanation{Node: n}
	for _, o := range others {
		x.Others = append(x.Others, o.ID)
	}
	pages := map[string]bool{}
	mentionPages := map[string]bool{}
	for _, e := range g.In(n.ID) {
		if e.Type != EDefines && e.Type != EDocuments {
			mentionPages[e.From] = true
		}
		switch e.Type {
		case EDefines:
			x.Defines = append(x.Defines, e)
		case EDocuments:
			x.Documents = append(x.Documents, e)
		case EMentions, ETaggedWith, EHasType, EForAudience, EInVersion, EOwnedBy, ELinksTo:
			x.Mentions = append(x.Mentions, e)
		default:
			continue
		}
		pages[e.From] = true
	}
	x.MentionPages = len(mentionPages)
	for k, v := range n.Props {
		if s, ok := strings.CutPrefix(k, "spelling:"); ok {
			if x.Spellings == nil {
				x.Spellings = map[string]int{}
			}
			x.Spellings[s], _ = strconv.Atoi(v)
		}
	}
	co, tags := map[string]int{}, map[string]int{}
	for p := range pages {
		counted := map[string]bool{}
		for _, e := range g.Out(p) {
			if counted[e.To] {
				continue
			}
			counted[e.To] = true
			if e.To == n.ID {
				continue
			}
			t := g.Node(e.To)
			if t == nil {
				continue
			}
			switch {
			case t.Type == TEntity && (e.Type == EMentions || e.Type == EDefines || e.Type == EDocuments):
				co[e.To]++
			case e.Type == ETaggedWith:
				tags[e.To]++
			}
		}
	}
	x.CoMentioned = g.topRelated(co, 8)
	x.Tags = g.topRelated(tags, 8)
	for _, r := range g.Reviews {
		if in(n.ID, r.Nodes) {
			x.Reviews = append(x.Reviews, r)
		}
	}
	return x, nil
}

func (g *Graph) topRelated(m map[string]int, k int) []Related {
	var out []Related
	for id, c := range m {
		out = append(out, Related{id, g.Node(id).Label, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// WriteText prints the explanation for a person.
func (x *Explanation) WriteText(w io.Writer, g *Graph, limit int) {
	n := x.Node
	kind := n.Type
	if n.Kind != "" {
		kind += ", " + n.Kind
	}
	fmt.Fprintf(w, "%s (%s)\n", n.Label, kind)
	if len(n.Aliases) > 0 {
		fmt.Fprintf(w, "  Also written: %s\n", strings.Join(n.Aliases, ", "))
	}
	if d := n.Props["description"]; d != "" {
		fmt.Fprintf(w, "  Vocabulary: %s\n", d)
	}
	section := func(title string, es []Edge) {
		if len(es) == 0 {
			fmt.Fprintf(w, "\n%s: none\n", title)
			return
		}
		fmt.Fprintf(w, "\n%s (%d):\n", title, len(es))
		for i, e := range es {
			if limit > 0 && i == limit {
				fmt.Fprintf(w, "  … and %d more (use --format json for all)\n", len(es)-limit)
				break
			}
			loc := e.File
			if e.Line > 0 {
				loc += fmt.Sprintf(":%d", e.Line)
			}
			label := e.From
			if p := g.Node(e.From); p != nil {
				label = p.Label
			}
			fmt.Fprintf(w, "  %s  [%s, %s]\n", label, loc, e.Derivation)
			if e.Span != "" {
				fmt.Fprintf(w, "      %q\n", trim(e.Span, 110))
			}
		}
	}
	section("Defined by", x.Defines)
	section("Documented by", x.Documents)
	section("Mentioned by", x.Mentions)
	if len(x.Spellings) > 0 {
		var parts []string
		for s, c := range x.Spellings {
			parts = append(parts, fmt.Sprintf("%q ×%d", s, c))
		}
		sort.Strings(parts)
		fmt.Fprintf(w, "\nSpellings in prose: %s\n", strings.Join(parts, ", "))
	}
	if len(x.CoMentioned) > 0 {
		fmt.Fprintln(w, "\nOften on the same pages:")
		for _, r := range x.CoMentioned {
			fmt.Fprintf(w, "  %s (%d)\n", r.Label, r.Count)
		}
	}
	if len(x.Tags) > 0 {
		var parts []string
		for _, r := range x.Tags {
			parts = append(parts, fmt.Sprintf("%s (%d)", r.Label, r.Count))
		}
		fmt.Fprintf(w, "\nTags on those pages: %s\n", strings.Join(parts, ", "))
	}
	for _, r := range x.Reviews {
		fmt.Fprintf(w, "\nReview: %s\n", r.Message)
	}
	if len(x.Defines) == 0 && n.Type == TEntity {
		fmt.Fprintln(w, "\nNo page defines this. Consider a glossary entry or a reference page.")
	}
	if len(x.Others) > 0 {
		fmt.Fprintf(w, "\nOther matches: %s\n", strings.Join(x.Others, ", "))
	}
}

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
