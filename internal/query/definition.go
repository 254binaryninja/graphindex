package query

import (
	"database/sql"
	"fmt"
)

type DefinitionResult struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Signature string `json:"signature,omitempty"`
}

func GetDefinition(db *sql.DB, name string) (*DefinitionResult, error) {
	row := db.QueryRow(`
		SELECT s.name, s.kind, f.path, s.line_start, s.line_end, COALESCE(s.signature, '')
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE s.name = ?
		LIMIT 1`, name)

	var r DefinitionResult
	err := row.Scan(&r.Name, &r.Kind, &r.File, &r.LineStart, &r.LineEnd, &r.Signature)
	if err == sql.ErrNoRows {
		// Fallback: suffix match for qualified method names
		row = db.QueryRow(`
			SELECT s.name, s.kind, f.path, s.line_start, s.line_end, COALESCE(s.signature, '')
			FROM symbols s
			JOIN files f ON f.id = s.file_id
			WHERE s.name LIKE ?
			LIMIT 1`, "%."+name)
		err = row.Scan(&r.Name, &r.Kind, &r.File, &r.LineStart, &r.LineEnd, &r.Signature)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("symbol %q not found", name)
		}
		if err != nil {
			return nil, err
		}
		return &r, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
