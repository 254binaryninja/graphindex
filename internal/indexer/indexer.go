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

// IndexRepo performs a full two-pass index of the repository.
//
// Pass 1 (parallel): walk the tree, parse each file, write symbols and the
// raw (unresolved) edges into pending_edges. Edge targets are stored as
// names because cross-file references generally cannot be resolved until
// every file's symbols exist.
//
// Pass 2 (sequential): clear the resolved edges table and re-derive it
// from pending_edges with the now-complete symbol table. dep_count in
// module_stats is recomputed from the resolved edges, not from raw
// extraction counts.
func (idx *Indexer) IndexRepo() error {
	var paths []string
	err := filepath.Walk(idx.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "vendor", "node_modules", ".graphindex", "dist", "build", ".next", ".angular", ".turbo", "coverage", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		if supportedExts[strings.ToLower(filepath.Ext(path))] {
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
			_ = idx.indexFilePass1(p)
		}(path)
	}
	wg.Wait()

	return idx.resolveAllPending()
}

// IndexFile re-indexes a single file (called by the watcher). It writes
// the file's symbols and pending edges, then runs a targeted resolution:
// pending edges originating from this file, and pending edges from other
// files whose target name matches a symbol newly defined in this file.
func (idx *Indexer) IndexFile(path string) error {
	fileID, changed, err := idx.indexFilePass1Returning(path)
	if err != nil || !changed {
		return err
	}
	return idx.resolveForFile(fileID)
}

// indexFilePass1 reads, parses, and writes symbols + pending_edges for
// one file. It does not attempt edge resolution.
func (idx *Indexer) indexFilePass1(path string) error {
	_, _, err := idx.indexFilePass1Returning(path)
	return err
}

func (idx *Indexer) indexFilePass1Returning(path string) (int64, bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, false, err
	}
	hash := contentHash(content)

	var stored string
	idx.db.Read.QueryRow("SELECT hash FROM files WHERE path = ?", path).Scan(&stored)
	if stored == hash {
		return 0, false, nil
	}

	symbols, edges := parser.Parse(path, content)
	fileID, err := idx.writeSymbolsAndPending(path, hash, symbols, edges)
	return fileID, true, err
}

func (idx *Indexer) writeSymbolsAndPending(path, hash string, symbols []parser.Symbol, edges []parser.Edge) (int64, error) {
	tx, err := idx.db.Write.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var fileID int64
	err = tx.QueryRow(`
		INSERT INTO files (path, hash, last_indexed)
		VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET hash=excluded.hash, last_indexed=excluded.last_indexed
		RETURNING id`, path, hash, time.Now().Unix()).Scan(&fileID)
	if err != nil {
		return 0, err
	}

	// Drop the file's previous symbols (cascades to edges, pending_edges,
	// module_stats, and FTS rows we manage manually below).
	rows, err := tx.Query("SELECT id FROM symbols WHERE file_id = ?", fileID)
	if err == nil {
		var oldIDs []int64
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			oldIDs = append(oldIDs, id)
		}
		rows.Close()
		for _, id := range oldIDs {
			tx.Exec("DELETE FROM symbols_fts WHERE rowid = ?", id)
		}
	}
	tx.Exec("DELETE FROM symbols WHERE file_id = ?", fileID)

	symStmt, err := tx.Prepare("INSERT INTO symbols (file_id, name, kind, line_start, line_end, signature) VALUES (?,?,?,?,?,?)")
	if err != nil {
		return 0, err
	}
	defer symStmt.Close()

	ftsStmt, err := tx.Prepare("INSERT INTO symbols_fts (rowid, name) VALUES (?, ?)")
	if err != nil {
		return 0, err
	}
	defer ftsStmt.Close()

	symbolIDs := make(map[string]int64)
	fnCount := 0
	for _, s := range symbols {
		result, err := symStmt.Exec(fileID, s.Name, s.Kind, s.LineStart, s.LineEnd, s.Signature)
		if err != nil {
			continue
		}
		id, _ := result.LastInsertId()
		symbolIDs[s.Name] = id
		ftsStmt.Exec(id, s.Name)
		if s.Kind == "function" {
			fnCount++
		}
	}

	pendStmt, err := tx.Prepare("INSERT INTO pending_edges (file_id, from_symbol_id, to_name, kind) VALUES (?,?,?,?)")
	if err != nil {
		return 0, err
	}
	defer pendStmt.Close()

	for _, e := range edges {
		fromID, ok := symbolIDs[e.FromSymbol]
		if !ok {
			continue
		}
		pendStmt.Exec(fileID, fromID, e.ToSymbol, e.Kind)
	}

	// Initial module_stats. dep_count starts at 0 and is set during
	// resolution (full or targeted).
	tx.Exec(`
		INSERT OR REPLACE INTO module_stats (file_id, symbol_count, fn_count, dep_count, updated_at)
		VALUES (?, ?, ?, 0, ?)`,
		fileID, len(symbols), fnCount, time.Now().Unix())

	return fileID, tx.Commit()
}

// resolveAllPending wipes the resolved edges table and rebuilds it from
// every pending_edges row. Called once at the end of a full IndexRepo.
func (idx *Indexer) resolveAllPending() error {
	tx, err := idx.db.Write.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM edges"); err != nil {
		return err
	}

	rows, err := tx.Query("SELECT from_symbol_id, to_name, kind FROM pending_edges")
	if err != nil {
		return err
	}
	type pending struct {
		fromID int64
		toName string
		kind   string
	}
	var all []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.fromID, &p.toName, &p.kind); err != nil {
			continue
		}
		all = append(all, p)
	}
	rows.Close()

	insert, err := tx.Prepare("INSERT OR IGNORE INTO edges (from_symbol_id, to_symbol_id, edge_kind) VALUES (?,?,?)")
	if err != nil {
		return err
	}
	defer insert.Close()

	for _, p := range all {
		toID := resolveSymbolID(tx, p.toName)
		if toID == 0 {
			continue
		}
		insert.Exec(p.fromID, toID, p.kind)
	}

	if _, err := tx.Exec(`
		UPDATE module_stats
		SET dep_count = (
			SELECT COUNT(*) FROM edges e
			JOIN symbols s ON s.id = e.from_symbol_id
			WHERE s.file_id = module_stats.file_id
		)`); err != nil {
		return err
	}

	return tx.Commit()
}

// resolveForFile runs targeted edge resolution after a single file has
// been re-indexed: this file's outgoing pending edges, plus pending
// edges from any other file whose to_name now matches a symbol defined
// in this file.
func (idx *Indexer) resolveForFile(fileID int64) error {
	tx, err := idx.db.Write.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	insert, err := tx.Prepare("INSERT OR IGNORE INTO edges (from_symbol_id, to_symbol_id, edge_kind) VALUES (?,?,?)")
	if err != nil {
		return err
	}
	defer insert.Close()

	// Outgoing: pending edges from this file.
	rows, err := tx.Query("SELECT from_symbol_id, to_name, kind FROM pending_edges WHERE file_id = ?", fileID)
	if err != nil {
		return err
	}
	type pending struct {
		fromID int64
		toName string
		kind   string
	}
	var outgoing []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.fromID, &p.toName, &p.kind); err != nil {
			continue
		}
		outgoing = append(outgoing, p)
	}
	rows.Close()

	for _, p := range outgoing {
		toID := resolveSymbolID(tx, p.toName)
		if toID == 0 {
			continue
		}
		insert.Exec(p.fromID, toID, p.kind)
	}

	// Incoming: pending edges from other files that target one of this
	// file's symbol names. Exact match only — fuzzy matches will be
	// caught by the next full IndexRepo.
	inRows, err := tx.Query(`
		SELECT pe.from_symbol_id, s.id, pe.kind
		FROM pending_edges pe
		JOIN symbols s ON s.name = pe.to_name
		WHERE s.file_id = ? AND pe.file_id != ?`, fileID, fileID)
	if err != nil {
		return err
	}
	var incoming []struct {
		fromID, toID int64
		kind         string
	}
	for inRows.Next() {
		var r struct {
			fromID, toID int64
			kind         string
		}
		if err := inRows.Scan(&r.fromID, &r.toID, &r.kind); err != nil {
			continue
		}
		incoming = append(incoming, r)
	}
	inRows.Close()

	for _, r := range incoming {
		insert.Exec(r.fromID, r.toID, r.kind)
	}

	// Recompute dep_count for this file. Other files' counts may shift
	// slightly from incoming bindings; they get corrected on the next
	// full IndexRepo or when those files are themselves re-indexed.
	if _, err := tx.Exec(`
		UPDATE module_stats
		SET dep_count = (
			SELECT COUNT(*) FROM edges e
			JOIN symbols s ON s.id = e.from_symbol_id
			WHERE s.file_id = ?
		)
		WHERE file_id = ?`, fileID, fileID); err != nil {
		return err
	}

	return tx.Commit()
}

func resolveSymbolID(tx *sql.Tx, name string) int64 {
	var id int64

	if tx.QueryRow("SELECT id FROM symbols WHERE name = ? LIMIT 1", name).Scan(&id) == nil && id != 0 {
		return id
	}
	if tx.QueryRow("SELECT id FROM symbols WHERE name LIKE ? LIMIT 1", "%."+name).Scan(&id) == nil && id != 0 {
		return id
	}

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
