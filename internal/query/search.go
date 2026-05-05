package query

import (
	"database/sql"
)

type SymbolRow struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	File string `json:"file"`
	Line int    `json:"line"`
}

type SearchResult struct {
	Symbols []SymbolRow `json:"symbols"`
}

func SearchSymbols(db *sql.DB, query string, kind string) (*SearchResult, error) {
	var rows *sql.Rows
	var err error

	ftsQuery := query + "*"

	if kind == "" || kind == "any" {
		rows, err = db.Query(`
			SELECT s.name, s.kind, f.path, s.line_start
			FROM symbols_fts fts
			JOIN symbols s ON s.id = fts.rowid
			JOIN files f ON f.id = s.file_id
			WHERE symbols_fts MATCH ?
			LIMIT 20`, ftsQuery)
	} else {
		rows, err = db.Query(`
			SELECT s.name, s.kind, f.path, s.line_start
			FROM symbols_fts fts
			JOIN symbols s ON s.id = fts.rowid
			JOIN files f ON f.id = s.file_id
			WHERE symbols_fts MATCH ? AND s.kind = ?
			LIMIT 20`, ftsQuery, kind)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := &SearchResult{}
	for rows.Next() {
		var r SymbolRow
		if err := rows.Scan(&r.Name, &r.Kind, &r.File, &r.Line); err != nil {
			continue
		}
		result.Symbols = append(result.Symbols, r)
	}
	if result.Symbols == nil {
		result.Symbols = []SymbolRow{}
	}
	return result, nil
}
