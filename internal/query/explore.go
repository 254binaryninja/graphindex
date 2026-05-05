package query

import (
	"database/sql"
	"fmt"
	"time"
)

type ExploreResult struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	File      string   `json:"file"`
	LineStart int      `json:"line_start"`
	LineEnd   int      `json:"line_end"`
	Signature string   `json:"signature,omitempty"`
	Callers   []string `json:"callers"`
	Impact    struct {
		Total        int    `json:"total_dependents"`
		SafeToModify bool   `json:"safe_to_modify"`
		Reason       string `json:"reason"`
	} `json:"impact"`
	Module struct {
		SymbolCount int    `json:"symbol_count"`
		FnCount     int    `json:"fn_count"`
		DepCount    int    `json:"dep_count"`
		LastIndexed string `json:"last_indexed"`
	} `json:"module"`
}

func ExploreSymbol(db *sql.DB, name string) (*ExploreResult, error) {
	// 1. Get definition — exact match first, then suffix match (e.g., "login" matches "AuthService.login")
	row := db.QueryRow(`
		SELECT s.name, s.kind, f.path, s.line_start, s.line_end, COALESCE(s.signature, '')
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE s.name = ?
		LIMIT 1`, name)

	var r ExploreResult
	err := row.Scan(&r.Name, &r.Kind, &r.File, &r.LineStart, &r.LineEnd, &r.Signature)
	if err == sql.ErrNoRows {
		// Fallback: try suffix match for method names (e.g., "authenticate" → "AuthService.authenticate")
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
		// Use the resolved full name for impact query
		name = r.Name
	} else if err != nil {
		return nil, err
	}

	// 2. Get callers via impact query (depth 3)
	rows, err := db.Query(impactQuery, name, 3)
	if err == nil {
		defer rows.Close()
		seen := make(map[string]bool)
		for rows.Next() {
			var sName, sKind, sFile string
			var depth int
			if err := rows.Scan(&sName, &sKind, &sFile, &depth); err != nil {
				continue
			}
			entry := fmt.Sprintf("%s (%s) in %s", sName, sKind, sFile)
			if depth == 1 {
				r.Callers = append(r.Callers, entry)
			}
			seen[entry] = true
		}
		total := len(seen)
		r.Impact.Total = total
		if total == 0 {
			r.Impact.SafeToModify = true
			r.Impact.Reason = "No dependents — safe to modify"
		} else if total <= 3 {
			r.Impact.SafeToModify = true
			r.Impact.Reason = fmt.Sprintf("Low impact — %d dependent(s)", total)
		} else {
			r.Impact.SafeToModify = false
			r.Impact.Reason = fmt.Sprintf("High impact — %d dependent(s), review carefully", total)
		}
	}
	if r.Callers == nil {
		r.Callers = []string{}
	}

	// 3. Get module summary for the file containing this symbol
	var updatedAt int64
	modRow := db.QueryRow(`
		SELECT ms.symbol_count, ms.fn_count, ms.dep_count, ms.updated_at
		FROM module_stats ms
		JOIN files f ON f.id = ms.file_id
		WHERE f.path = ?`, r.File)
	if modRow.Scan(&r.Module.SymbolCount, &r.Module.FnCount, &r.Module.DepCount, &updatedAt) == nil {
		r.Module.LastIndexed = time.Unix(updatedAt, 0).UTC().Format(time.RFC3339)
	}

	return &r, nil
}
