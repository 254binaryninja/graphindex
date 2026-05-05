package query

import (
	"database/sql"
	"fmt"
	"time"
)

type ModuleSummary struct {
	File        string `json:"file"`
	SymbolCount int    `json:"symbol_count"`
	FnCount     int    `json:"fn_count"`
	DepCount    int    `json:"dep_count"`
	LastIndexed string `json:"last_indexed"`
}

func GetModuleSummary(db *sql.DB, filePath string) (*ModuleSummary, error) {
	row := db.QueryRow(`
		SELECT f.path, ms.symbol_count, ms.fn_count, ms.dep_count, ms.updated_at
		FROM module_stats ms
		JOIN files f ON f.id = ms.file_id
		WHERE f.path = ?`, filePath)

	var r ModuleSummary
	var updatedAt int64
	err := row.Scan(&r.File, &r.SymbolCount, &r.FnCount, &r.DepCount, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("file %q not indexed", filePath)
	}
	if err != nil {
		return nil, err
	}
	r.LastIndexed = time.Unix(updatedAt, 0).UTC().Format(time.RFC3339)
	return &r, nil
}
