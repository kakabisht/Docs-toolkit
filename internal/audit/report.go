package audit

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Markdown writes the report for people. items caps the items listed per
// dimension (0 for all).
func (r *Report) Markdown(w io.Writer, items int) {
	fmt.Fprintf(w, "# Docs audit: %s\n\n", r.Source)
	var parts []string
	for _, p := range []string{Structure, Findability} {
		if s, ok := r.PartScores[p]; ok {
			parts = append(parts, fmt.Sprintf("%s %.0f", p, s))
		}
	}
	fmt.Fprintf(w, "**Score: %.0f/100** (%s) across %d pages.\n\n", r.Score, strings.Join(parts, ", "), r.Pages)
	fmt.Fprintln(w, "Scores come from deterministic rules with the weights listed at the end. They are indicators for deciding what to fix first, not measurements of reader success.")
	if len(r.FixFirst) > 0 {
		fmt.Fprintln(w, "\n## Fix first")
		fmt.Fprintln(w)
		for i, it := range r.FixFirst {
			fmt.Fprintf(w, "%d. %s%s _(%s)_\n", i+1, loc(it), it.Message, it.Dimension)
		}
	}
	for _, part := range []string{Structure, Findability} {
		var ds []*Dimension
		for _, d := range r.Dimensions {
			if d.Part == part {
				ds = append(ds, d)
			}
		}
		if len(ds) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n## %s\n\n", strings.ToUpper(part[:1])+part[1:])
		fmt.Fprintln(w, "| Dimension | Score | Weight | Summary |")
		fmt.Fprintln(w, "| --- | ---: | ---: | --- |")
		for _, d := range ds {
			score := fmt.Sprintf("%.0f", d.Score)
			if !d.Scored {
				score = "not scored"
			}
			fmt.Fprintf(w, "| %s | %s | %g | %s |\n", d.Name, score, d.Weight, cell(d.Summary))
		}
		for _, d := range ds {
			fmt.Fprintf(w, "\n### %s\n\n", d.Name)
			fmt.Fprintf(w, "_How it is scored:_ %s\n", d.Method)
			if len(d.Metrics) > 0 {
				fmt.Fprintln(w)
				for _, m := range d.Metrics {
					fmt.Fprintf(w, "- %s: %s\n", m.Name, m.Value)
				}
			}
			if len(d.Items) > 0 {
				fmt.Fprintf(w, "\n%d item%s", len(d.Items), plural(len(d.Items)))
				if items > 0 && len(d.Items) > items {
					fmt.Fprintf(w, ", the %d with most impact (JSON output lists all)", items)
				}
				fmt.Fprintln(w, ":")
				fmt.Fprintln(w)
				for i, it := range d.Items {
					if items > 0 && i == items {
						break
					}
					fmt.Fprintf(w, "- %s%s\n", loc(it), it.Message)
				}
			}
		}
	}
	if len(r.Tasks) > 0 {
		fmt.Fprintln(w, "\n## Tasks")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Task | Search | Clicks | Queries |")
		fmt.Fprintln(w, "| --- | --- | ---: | --- |")
		for _, t := range r.Tasks {
			status := "found"
			if !t.Found {
				status = "**not found**"
			}
			clicks := "unreachable"
			if t.Clicks >= 0 {
				clicks = fmt.Sprint(t.Clicks)
			}
			var qs []string
			for _, q := range t.Queries {
				rank := "not in top 10"
				if q.Rank > 0 {
					rank = fmt.Sprintf("rank %d", q.Rank)
				}
				qs = append(qs, fmt.Sprintf("%q %s", q.Query, rank))
			}
			if t.Problem != "" {
				qs = []string{t.Problem}
			}
			fmt.Fprintf(w, "| %s | %s | %s | %s |\n", cell(t.Task), status, clicks, cell(strings.Join(qs, "; ")))
		}
	}
	if len(r.AgentSignal) > 0 {
		fmt.Fprintln(w, "\n## Agent summary")
		fmt.Fprintln(w)
		for _, m := range r.AgentSignal {
			fmt.Fprintf(w, "- %s: %s\n", m.Name, m.Value)
		}
	}
	fmt.Fprintln(w, "\n## Weights")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Each part's score is the weighted mean of its scored dimensions; the overall score is the weighted mean of all of them. Change weights with `audit.weights` in the config.")
	fmt.Fprintln(w)
	keys := make([]string, 0, len(r.Weights))
	for _, d := range r.Dimensions {
		keys = append(keys, d.ID)
	}
	sort.Strings(keys)
	var ws []string
	for _, k := range keys {
		ws = append(ws, fmt.Sprintf("%s %g", k, r.Weights[k]))
	}
	fmt.Fprintln(w, strings.Join(ws, ", "))
	if len(r.Notes) > 0 {
		fmt.Fprintln(w, "\n## Notes")
		fmt.Fprintln(w)
		for _, n := range r.Notes {
			fmt.Fprintf(w, "- %s\n", n)
		}
	}
}

func loc(it Item) string {
	switch {
	case it.File != "" && it.Line > 0:
		return fmt.Sprintf("`%s:%d` ", it.File, it.Line)
	case it.File != "":
		return fmt.Sprintf("`%s` ", it.File)
	}
	return ""
}

func cell(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ") }

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
