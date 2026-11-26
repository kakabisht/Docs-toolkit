package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite" // pure Go driver, no cgo

	"github.com/docsbot/docsbot/internal/model"
)

const schema = `
CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT);
CREATE TABLE pages (
  key TEXT PRIMARY KEY, url TEXT, path TEXT, title TEXT, description TEXT, format TEXT, source TEXT,
  words INT, chars INT, status INT, final_url TEXT, draft INT, error TEXT,
  rank REAL, depth INT, in_links INT, out_links INT, in_llms_txt INT, in_sitemap INT
);
CREATE TABLE sections (
  page_key TEXT, ord INT, level INT, text TEXT, anchor TEXT, line INT,
  PRIMARY KEY (page_key, ord)
);
CREATE TABLE anchors (page_key TEXT, anchor TEXT, PRIMARY KEY (page_key, anchor));
CREATE TABLE code_blocks (page_key TEXT, ord INT, lang TEXT, lines INT, line INT, PRIMARY KEY (page_key, ord));
CREATE TABLE links (
  id INTEGER PRIMARY KEY, src_key TEXT, src_section TEXT, line INT, href TEXT, text TEXT,
  kind TEXT, dst_key TEXT, dst_url TEXT, fragment TEXT, dst_exists INT, via_alias INT, image INT, nav INT
);
CREATE INDEX links_src ON links(src_key);
CREATE INDEX links_dst ON links(dst_key);
CREATE INDEX links_kind ON links(kind);
-- a node/edge view for graph tools: pages and sections are nodes
CREATE VIEW nodes AS
  SELECT key AS id, 'page' AS type, title AS label, key AS page_key FROM pages
  UNION ALL
  SELECT page_key || '#' || anchor, 'section', text, page_key FROM sections;
CREATE VIEW edges AS
  SELECT page_key AS src, page_key || '#' || anchor AS dst, 'contains' AS type FROM sections
  UNION ALL
  SELECT CASE WHEN src_section <> '' THEN src_key || '#' || src_section ELSE src_key END,
         CASE WHEN fragment <> '' THEN dst_key || '#' || fragment ELSE dst_key END,
         CASE WHEN nav = 1 THEN 'nav' ELSE 'links_to' END
  FROM links WHERE kind IN ('internal','anchor') AND dst_exists = 1;
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
		return fmt.Errorf("schema: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	llms, _ := json.Marshal(g.LLMSTxt)
	for k, v := range map[string]string{
		"mode": g.Mode, "source": g.Source, "base_url": g.BaseURL, "entry": g.Entry,
		"built": g.Built.Format(time.RFC3339), "llms_txt": string(llms), "schema_version": "1",
	} {
		if _, err := tx.Exec(`INSERT INTO meta VALUES (?,?)`, k, v); err != nil {
			return err
		}
	}
	pst, _ := tx.Prepare(`INSERT INTO pages VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	sst, _ := tx.Prepare(`INSERT INTO sections VALUES (?,?,?,?,?,?)`)
	ast, _ := tx.Prepare(`INSERT OR IGNORE INTO anchors VALUES (?,?)`)
	cst, _ := tx.Prepare(`INSERT INTO code_blocks VALUES (?,?,?,?,?)`)
	lst, _ := tx.Prepare(`INSERT INTO links (src_key,src_section,line,href,text,kind,dst_key,dst_url,fragment,dst_exists,via_alias,image,nav) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	for _, n := range g.Nodes {
		if _, err := pst.Exec(n.Key, n.URL, n.Path, n.Title, n.Description, n.Format, n.Source, n.Words, n.Chars, n.Status,
			n.FinalURL, b2i(n.Draft), n.Err, n.Rank, n.Depth, n.In, n.Out, b2i(n.InLLMSTxt), b2i(n.InSitemap)); err != nil {
			return fmt.Errorf("page %s: %w", n.Key, err)
		}
		for i, h := range n.Sections {
			if _, err := sst.Exec(n.Key, i, h.Level, h.Text, h.Anchor, h.Line); err != nil {
				return err
			}
		}
		for a := range n.Anchors {
			if _, err := ast.Exec(n.Key, a); err != nil {
				return err
			}
		}
		for i, c := range n.Code {
			if _, err := cst.Exec(n.Key, i, c.Lang, c.Lines, c.Line); err != nil {
				return err
			}
		}
	}
	for _, e := range g.Edges {
		if _, err := lst.Exec(e.From, e.Section, e.Line, e.Href, e.Text, e.Kind, e.To, e.URL, e.Fragment, b2i(e.Exists), b2i(e.ViaAlias), b2i(e.Image), b2i(e.Nav)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Load reads a graph saved by Save.
func Load(path string) (*Graph, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("graph database %s not found (run `docsbot graph build` first): %w", path, err)
	}
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	g := &Graph{}
	rows, err := db.Query(`SELECT k, v FROM meta`)
	if err != nil {
		return nil, fmt.Errorf("%s is not a docsbot graph: %w", path, err)
	}
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		switch k {
		case "mode":
			g.Mode = v
		case "source":
			g.Source = v
		case "base_url":
			g.BaseURL = v
		case "entry":
			g.Entry = v
		case "built":
			g.Built, _ = time.Parse(time.RFC3339, v)
		case "llms_txt":
			json.Unmarshal([]byte(v), &g.LLMSTxt)
		}
	}
	rows.Close()

	rows, err = db.Query(`SELECT key,url,path,title,description,format,source,words,chars,status,final_url,draft,error,rank,depth,in_links,out_links,in_llms_txt,in_sitemap FROM pages ORDER BY key`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		n := &Node{Anchors: map[string]bool{}}
		var draft, inl, ins int
		if err := rows.Scan(&n.Key, &n.URL, &n.Path, &n.Title, &n.Description, &n.Format, &n.Source, &n.Words, &n.Chars, &n.Status,
			&n.FinalURL, &draft, &n.Err, &n.Rank, &n.Depth, &n.In, &n.Out, &inl, &ins); err != nil {
			return nil, err
		}
		n.Draft, n.InLLMSTxt, n.InSitemap = draft == 1, inl == 1, ins == 1
		g.Nodes = append(g.Nodes, n)
	}
	rows.Close()

	rows, err = db.Query(`SELECT page_key, level, text, anchor, line FROM sections ORDER BY page_key, ord`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		var h model.Heading
		rows.Scan(&k, &h.Level, &h.Text, &h.Anchor, &h.Line)
		if n := g.Node(k); n != nil {
			n.Sections = append(n.Sections, h)
		}
	}
	rows.Close()

	rows, err = db.Query(`SELECT page_key, anchor FROM anchors`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, a string
		rows.Scan(&k, &a)
		if n := g.Node(k); n != nil {
			n.Anchors[a] = true
		}
	}
	rows.Close()

	rows, err = db.Query(`SELECT page_key, lang, lines, line FROM code_blocks ORDER BY page_key, ord`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		var c model.CodeBlock
		rows.Scan(&k, &c.Lang, &c.Lines, &c.Line)
		if n := g.Node(k); n != nil {
			n.Code = append(n.Code, c)
		}
	}
	rows.Close()

	rows, err = db.Query(`SELECT src_key,src_section,line,href,text,kind,dst_key,dst_url,fragment,dst_exists,via_alias,image,nav FROM links ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e Edge
		var ex, va, im, nv int
		rows.Scan(&e.From, &e.Section, &e.Line, &e.Href, &e.Text, &e.Kind, &e.To, &e.URL, &e.Fragment, &ex, &va, &im, &nv)
		e.Exists, e.ViaAlias, e.Image, e.Nav = ex == 1, va == 1, im == 1, nv == 1
		g.Edges = append(g.Edges, e)
	}
	rows.Close()
	return g, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
