package kg

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	_ "modernc.org/sqlite" // pure Go driver, no cgo
)

// SchemaVersion changes when the tables change.
const SchemaVersion = "kg-1"

const schema = `
CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT);
CREATE TABLE nodes (id TEXT PRIMARY KEY, type TEXT NOT NULL, kind TEXT, label TEXT, aliases TEXT, props TEXT);
CREATE TABLE edges (
  id INTEGER PRIMARY KEY, src TEXT NOT NULL, dst TEXT NOT NULL, type TEXT NOT NULL,
  derivation TEXT NOT NULL, file TEXT, line INT, span TEXT, confidence REAL, broken INT
);
CREATE TABLE reviews (id INTEGER PRIMARY KEY, kind TEXT, nodes TEXT, message TEXT);
CREATE INDEX nodes_type ON nodes(type);
CREATE INDEX edges_src ON edges(src);
CREATE INDEX edges_dst ON edges(dst);
CREATE INDEX edges_type ON edges(type);
`

// Save writes the graph to a fresh SQLite file at path.
func (g *Graph) Save(path string) error {
	tmp := path + ".tmp"
	os.Remove(tmp)
	db, err := sql.Open("sqlite", tmp)
	if err != nil {
		return err
	}
	if err := g.write(db); err != nil {
		db.Close()
		os.Remove(tmp)
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (g *Graph) write(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	meta := map[string]string{"schema": SchemaVersion, "source": g.Source, "entry": g.Entry}
	for k, v := range g.Meta {
		meta["m:"+k] = v
	}
	for k, v := range meta {
		if _, err := tx.Exec(`INSERT INTO meta VALUES (?, ?)`, k, v); err != nil {
			return err
		}
	}
	ns, err := tx.Prepare(`INSERT INTO nodes VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	for _, n := range g.Nodes {
		al, _ := json.Marshal(n.Aliases)
		pr, _ := json.Marshal(n.Props)
		if _, err := ns.Exec(n.ID, n.Type, n.Kind, n.Label, string(al), string(pr)); err != nil {
			return fmt.Errorf("node %s: %w", n.ID, err)
		}
	}
	es, err := tx.Prepare(`INSERT INTO edges (src, dst, type, derivation, file, line, span, confidence, broken) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	for _, e := range g.Edges {
		b := 0
		if e.Broken {
			b = 1
		}
		if _, err := es.Exec(e.From, e.To, e.Type, e.Derivation, e.File, e.Line, e.Span, e.Confidence, b); err != nil {
			return err
		}
	}
	for _, r := range g.Reviews {
		ids, _ := json.Marshal(r.Nodes)
		if _, err := tx.Exec(`INSERT INTO reviews (kind, nodes, message) VALUES (?, ?, ?)`, r.Kind, string(ids), r.Message); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Load reads a graph written by Save.
func Load(path string) (*Graph, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	g := &Graph{Meta: map[string]string{}}
	rows, err := db.Query(`SELECT k, v FROM meta`)
	if err != nil {
		return nil, fmt.Errorf("%s is not a knowledge graph file (run graph build again): %w", path, err)
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		switch {
		case k == "source":
			g.Source = v
		case k == "entry":
			g.Entry = v
		case k == "schema":
			if v != SchemaVersion {
				rows.Close()
				return nil, fmt.Errorf("%s was written by another version (schema %s, want %s): run graph build again", path, v, SchemaVersion)
			}
		case len(k) > 2 && k[:2] == "m:":
			g.Meta[k[2:]] = v
		}
	}
	rows.Close()
	rows, err = db.Query(`SELECT id, type, kind, label, aliases, props FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		n := &Node{}
		var al, pr string
		if err := rows.Scan(&n.ID, &n.Type, &n.Kind, &n.Label, &al, &pr); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(al), &n.Aliases)
		json.Unmarshal([]byte(pr), &n.Props)
		g.Nodes = append(g.Nodes, n)
	}
	rows.Close()
	rows, err = db.Query(`SELECT src, dst, type, derivation, file, line, span, confidence, broken FROM edges ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e Edge
		var b int
		if err := rows.Scan(&e.From, &e.To, &e.Type, &e.Derivation, &e.File, &e.Line, &e.Span, &e.Confidence, &b); err != nil {
			return nil, err
		}
		e.Broken = b == 1
		g.Edges = append(g.Edges, e)
	}
	rows.Close()
	rows, err = db.Query(`SELECT kind, nodes, message FROM reviews ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r Review
		var ids string
		if err := rows.Scan(&r.Kind, &ids, &r.Message); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(ids), &r.Nodes)
		g.Reviews = append(g.Reviews, r)
	}
	rows.Close()
	g.reindex()
	return g, nil
}
