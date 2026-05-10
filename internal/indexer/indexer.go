package indexer

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/254binaryninja/graphindex/internal/db"
	"github.com/254binaryninja/graphindex/internal/parser"
)

var supportedExts map[string]bool

func init() {
	supportedExts = make(map[string]bool)
	for _, ext := range parser.SupportedExtensions() {
		supportedExts[ext] = true
	}
}

type Indexer struct {
	db   *db.DB
	root string
}

func New(database *db.DB, root string) *Indexer {
	return &Indexer{db: database, root: root}
}

func (idx *Indexer) IndexRepo() error {
	var paths []string
	err := filepath.Walk(idx.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			switch name {
			case ".git", "vendor", "node_modules", ".graphindex", "dist", "build", ".next", ".angular", ".turbo", "coverage", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if supportedExts[ext] {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return err
	}

	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup

	for _, path := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			_ = idx.IndexFile(p)
		}(path)
	}
	wg.Wait()
	return nil
}

func (idx *Indexer) IndexFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	hash := contentHash(content)

	var stored string
	row := idx.db.Read.QueryRow("SELECT hash FROM files WHERE path = ?", path)
	_ = row.Scan(&stored)
	if stored == hash {
		return nil
	}

	symbols, edges := parser.Parse(path, content)
	return idx.writeFileData(path, hash, symbols, edges)
}

func (idx *Indexer) writeFileData(path, hash string, symbols []parser.Symbol, edges []parser.Edge) error {
	tx, err := idx.db.Write.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var fileID int64
	err = tx.QueryRow(`
		INSERT INTO files (path, hash, last_indexed)
		VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET hash=excluded.hash, last_indexed=excluded.last_indexed
		RETURNING id`, path, hash, time.Now().Unix()).Scan(&fileID)
	if err != nil {
		return err
	}

	// Get existing symbol IDs for FTS cleanup
	rows, err := tx.Query("SELECT id FROM symbols WHERE file_id = ?", fileID)
	if err == nil {
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			tx.Exec("DELETE FROM symbols_fts WHERE rowid = ?", id)
		}
		rows.Close()
	}

	tx.Exec("DELETE FROM edges WHERE from_symbol_id IN (SELECT id FROM symbols WHERE file_id = ?)", fileID)
	tx.Exec("DELETE FROM symbols WHERE file_id = ?", fileID)

	symStmt, err := tx.Prepare("INSERT INTO symbols (file_id, name, kind, line_start, line_end, signature) VALUES (?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer symStmt.Close()

	ftsStmt, err := tx.Prepare("INSERT INTO symbols_fts (rowid, name) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer ftsStmt.Close()

	symbolIDs := make(map[string]int64)
	for _, s := range symbols {
		result, err := symStmt.Exec(fileID, s.Name, s.Kind, s.LineStart, s.LineEnd, s.Signature)
		if err != nil {
			continue
		}
		id, _ := result.LastInsertId()
		symbolIDs[s.Name] = id
		ftsStmt.Exec(id, s.Name)
	}

	edgeStmt, err := tx.Prepare("INSERT INTO edges (from_symbol_id, to_symbol_id, edge_kind) VALUES (?,?,?)")
	if err != nil {
		return err
	}
	defer edgeStmt.Close()

	for _, e := range edges {
		fromID, ok := symbolIDs[e.FromSymbol]
		if !ok {
			continue
		}
		toID, ok := symbolIDs[e.ToSymbol]
		if !ok {
			toID = resolveSymbolID(tx, e.ToSymbol)
			if toID == 0 {
				continue
			}
		}
		edgeStmt.Exec(fromID, toID, e.Kind)
	}

	fnCount := 0
	for _, s := range symbols {
		if s.Kind == "function" {
			fnCount++
		}
	}
	tx.Exec(`
		INSERT OR REPLACE INTO module_stats (file_id, symbol_count, fn_count, dep_count, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		fileID, len(symbols), fnCount, len(edges), time.Now().Unix())

	return tx.Commit()
}

func resolveSymbolID(tx *sql.Tx, name string) int64 {
	var id int64

	// Try exact match first
	if tx.QueryRow("SELECT id FROM symbols WHERE name = ? LIMIT 1", name).Scan(&id) == nil && id != 0 {
		return id
	}

	// Try suffix match (e.g., "authenticate" matches "AuthService.authenticate")
	if tx.QueryRow("SELECT id FROM symbols WHERE name LIKE ? LIMIT 1", "%."+name).Scan(&id) == nil && id != 0 {
		return id
	}

	// Strip selector prefix (e.g., "pkg.Func" -> "Func") and try again
	parts := strings.Split(name, ".")
	searchName := parts[len(parts)-1]
	if searchName != name {
		if tx.QueryRow("SELECT id FROM symbols WHERE name = ? LIMIT 1", searchName).Scan(&id) == nil && id != 0 {
			return id
		}
		tx.QueryRow("SELECT id FROM symbols WHERE name LIKE ? LIMIT 1", "%."+searchName).Scan(&id)
	}

	return id
}

func (idx *Indexer) RemoveFile(path string) error {
	_, err := idx.db.Write.Exec("DELETE FROM files WHERE path = ?", path)
	return err
}
