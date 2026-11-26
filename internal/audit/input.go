package audit

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Task is a reader goal with the searches they might type and the pages
// that answer it.
type Task struct {
	Task    string   `yaml:"task" json:"task"`
	Queries []string `yaml:"queries" json:"queries"`
	Expect  []string `yaml:"expect" json:"expect"`         // routes, file paths or URLs; any one answers the task
	Start   string   `yaml:"start" json:"start,omitempty"` // page the reader starts on (default: the entry page)
}

// Query is a search from a query log.
type Query struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}

// LoadTasks reads a tasks file:
//
//	tasks:
//	  - task: Install on Windows
//	    queries: [install windows, windows installer]
//	    expect: [install/windows.md]
func LoadTasks(path string) ([]Task, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Tasks []Task `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for i, t := range f.Tasks {
		if t.Task == "" || len(t.Expect) == 0 {
			return nil, fmt.Errorf("%s: task %d needs task and expect", path, i+1)
		}
		if len(t.Queries) == 0 {
			f.Tasks[i].Queries = []string{t.Task}
		}
	}
	return f.Tasks, nil
}

// LoadQueryLog reads a CSV of searches. It finds the query column by a
// header containing "query", "term", "search" or "keyword", and the count
// column by "count", "searches", "volume", "hits" or "sessions"; without a
// header, the first column is the query and the second the count.
func LoadQueryLog(path string) ([]Query, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readQueryLog(f)
}

func readQueryLog(r io.Reader) ([]Query, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	qi, ci := 0, 1
	start := 0
	head := rows[0]
	if len(head) > 1 {
		if _, err := strconv.Atoi(strings.TrimSpace(head[1])); err != nil {
			start = 1
			qi, ci = -1, -1
			for i, h := range head {
				h = strings.ToLower(h)
				switch {
				case qi < 0 && (strings.Contains(h, "query") || strings.Contains(h, "term") || strings.Contains(h, "search") && !strings.Contains(h, "searches") || strings.Contains(h, "keyword")):
					qi = i
				case ci < 0 && (strings.Contains(h, "count") || strings.Contains(h, "searches") || strings.Contains(h, "volume") || strings.Contains(h, "hits") || strings.Contains(h, "sessions")):
					ci = i
				}
			}
			if qi < 0 {
				qi = 0
			}
		}
	} else {
		ci = -1
	}
	merged := map[string]int{}
	var order []string
	for _, row := range rows[start:] {
		if qi >= len(row) {
			continue
		}
		q := strings.Join(strings.Fields(strings.ToLower(row[qi])), " ")
		if q == "" {
			continue
		}
		n := 1
		if ci >= 0 && ci < len(row) {
			if v, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(row[ci]), ",", "")); err == nil {
				n = v
			}
		}
		if _, ok := merged[q]; !ok {
			order = append(order, q)
		}
		merged[q] += n
	}
	out := make([]Query, 0, len(order))
	for _, q := range order {
		out = append(out, Query{q, merged[q]})
	}
	return out, nil
}
