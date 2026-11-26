package kg

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strings"
)

//go:embed viewer.html
var viewerHTML string

var viewer = template.Must(template.New("viewer").Parse(viewerHTML))

// HTML writes a self-contained, offline viewer: no external scripts, fonts
// or requests.
func (g *Graph) HTML(w io.Writer) error {
	data, err := json.Marshal(struct {
		Nodes []*Node `json:"nodes"`
		Edges []Edge  `json:"edges"`
	}{g.Nodes, g.Edges})
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, n := range g.Nodes {
		counts[n.Type]++
	}
	var parts []string
	for t, c := range counts {
		parts = append(parts, fmt.Sprintf("%d %s", c, t))
	}
	sort.Strings(parts)
	return viewer.Execute(w, map[string]any{
		"Title":   g.Source,
		"Summary": fmt.Sprintf("%d nodes (%s) and %d edges", len(g.Nodes), strings.Join(parts, ", "), len(g.Edges)),
		"Data":    template.JS(data),
	})
}
