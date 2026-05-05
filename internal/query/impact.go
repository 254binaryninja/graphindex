package query

import (
	"database/sql"
	"fmt"
)

type ImpactResult struct {
	Target         string   `json:"target"`
	DirectCallers  []string `json:"direct_callers"`
	TransitiveRisk []string `json:"transitive_risk"`
	SafeToModify   bool     `json:"safe_to_modify"`
	Reason         string   `json:"reason"`
}

const impactQuery = `
WITH RECURSIVE dependents(symbol_id, depth) AS (
    SELECT from_symbol_id, 1
    FROM edges
    WHERE to_symbol_id = (SELECT id FROM symbols WHERE name = ? LIMIT 1)

    UNION ALL

    SELECT e.from_symbol_id, d.depth + 1
    FROM edges e
    JOIN dependents d ON e.to_symbol_id = d.symbol_id
    WHERE d.depth < ?
)
SELECT DISTINCT s.name, s.kind, f.path, d.depth
FROM dependents d
JOIN symbols s ON s.id = d.symbol_id
JOIN files   f ON f.id = s.file_id
ORDER BY d.depth, s.name
`

func AnalyzeImpact(db *sql.DB, symbolName string, maxDepth int) (*ImpactResult, error) {
	if maxDepth <= 0 || maxDepth > 4 {
		maxDepth = 3
	}

	rows, err := db.Query(impactQuery, symbolName, maxDepth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := &ImpactResult{
		Target:         symbolName,
		DirectCallers:  []string{},
		TransitiveRisk: []string{},
	}

	for rows.Next() {
		var name, kind, file string
		var depth int
		if err := rows.Scan(&name, &kind, &file, &depth); err != nil {
			continue
		}
		entry := fmt.Sprintf("%s (%s) in %s", name, kind, file)
		if depth == 1 {
			result.DirectCallers = append(result.DirectCallers, entry)
		} else {
			result.TransitiveRisk = append(result.TransitiveRisk, entry)
		}
	}

	total := len(result.DirectCallers) + len(result.TransitiveRisk)
	if total == 0 {
		result.SafeToModify = true
		result.Reason = "No dependents found — safe to modify"
	} else if total <= 3 {
		result.SafeToModify = true
		result.Reason = fmt.Sprintf("Low impact — %d dependent(s)", total)
	} else {
		result.SafeToModify = false
		result.Reason = fmt.Sprintf("High impact — %d dependent(s), review carefully", total)
	}

	return result, nil
}
