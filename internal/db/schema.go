package db

import "database/sql"

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous  = NORMAL;
PRAGMA cache_size   = -64000;
PRAGMA temp_store   = MEMORY;

CREATE TABLE IF NOT EXISTS files (
  id           INTEGER PRIMARY KEY,
  path         TEXT    NOT NULL UNIQUE,
  hash         TEXT    NOT NULL,
  last_indexed INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS symbols (
  id         INTEGER PRIMARY KEY,
  file_id    INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  name       TEXT    NOT NULL,
  kind       TEXT    NOT NULL,
  line_start INTEGER NOT NULL,
  line_end   INTEGER NOT NULL,
  signature  TEXT
);

CREATE TABLE IF NOT EXISTS edges (
  id             INTEGER PRIMARY KEY,
  from_symbol_id INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
  to_symbol_id   INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
  edge_kind      TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS pending_edges (
  id             INTEGER PRIMARY KEY,
  file_id        INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  from_symbol_id INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
  to_name        TEXT    NOT NULL,
  kind           TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS module_stats (
  file_id       INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
  symbol_count  INTEGER NOT NULL DEFAULT 0,
  fn_count      INTEGER NOT NULL DEFAULT 0,
  dep_count     INTEGER NOT NULL DEFAULT 0,
  updated_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file_id, name);
CREATE INDEX IF NOT EXISTS idx_edges_from   ON edges(from_symbol_id);
CREATE INDEX IF NOT EXISTS idx_edges_to     ON edges(to_symbol_id);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_edges ON edges(from_symbol_id, to_symbol_id, edge_kind);
CREATE INDEX IF NOT EXISTS idx_pending_to   ON pending_edges(to_name);
CREATE INDEX IF NOT EXISTS idx_pending_file ON pending_edges(file_id);

CREATE VIRTUAL TABLE IF NOT EXISTS symbols_fts
  USING fts5(name, content='symbols', content_rowid='id');
`

func Migrate(db *sql.DB) error {
	_, err := db.Exec(schema)
	return err
}
