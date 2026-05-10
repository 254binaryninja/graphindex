# graphindex

An MCP server that gives AI coding assistants a structured, queryable view of your codebase. graphindex parses your repository with [tree-sitter](https://tree-sitter.github.io/), stores symbols and call relationships in a local SQLite database, and exposes them through the [Model Context Protocol](https://modelcontextprotocol.io/) so tools like Claude Code can navigate code by *meaning* instead of by `grep`.

The index updates incrementally as files change, so it stays accurate while you work.

## Why

LLM agents spend a lot of tokens reading files just to figure out where a symbol lives or who calls it. graphindex answers those questions directly:

- "Where is `parseConfig` defined?" → one tool call, one row.
- "What breaks if I change this function?" → transitive callers up to depth 3.
- "Give me a summary of this file." → counts and last-indexed time without opening it.

## Supported languages

- Go
- TypeScript / JavaScript
- Angular templates

More can be added by dropping a tree-sitter grammar binding into `internal/parser/`.

## Install

```bash
go install github.com/254binaryninja/graphindex@latest
```

This places a `graphindex` binary in `$GOBIN` (usually `~/go/bin`). Make sure that directory is on your `PATH`.

Requires Go 1.26 or newer.

## Usage

graphindex speaks MCP over stdio. You normally don't run it by hand — you point an MCP client at it.

### With Claude Code

Add it to your MCP config (`~/.claude/mcp.json` or project-local `.mcp.json`):

```json
{
  "mcpServers": {
    "graphindex": {
      "command": "graphindex",
      "args": ["--repo", "/absolute/path/to/your/repo"]
    }
  }
}
```

### Flags

| Flag    | Default                    | Description                              |
| ------- | -------------------------- | ---------------------------------------- |
| `--repo` | `.`                       | Path to the repository root to index     |
| `--db`   | `<repo>/.graphindex.db`   | Path to the SQLite database file         |

On startup graphindex performs a full index, then watches the repo for changes via `fsnotify` and re-indexes touched files in the background.

Add `.graphindex.db` to your `.gitignore`.

## MCP tools exposed

| Tool                 | Purpose                                                                                  |
| -------------------- | ---------------------------------------------------------------------------------------- |
| `search_symbols`     | Find functions, classes, variables, and types by name. Optional `kind` filter.            |
| `get_definition`     | Return the source location and signature for an exact symbol name.                       |
| `analyze_impact`     | Direct callers and transitive dependents up to depth 3 — blast radius before refactors. |
| `get_module_summary` | Per-file symbol/function/dependency counts and last index time.                          |
| `explore_symbol`     | One-shot: definition + signature + callers + impact + module stats in a single call.     |

## How it works

```
                ┌──────────────────────┐
                │  fsnotify watcher    │
                └──────────┬───────────┘
                           │ file events
                           ▼
   ┌─────────────────────────────────────────────┐
   │  indexer  →  tree-sitter parser per language │
   └─────────────────────┬───────────────────────┘
                         │ symbols, refs, edges
                         ▼
                 ┌────────────────┐         ┌─────────────┐
                 │  SQLite (WAL)  │ ◀────── │  query/*    │
                 └────────────────┘         └──────┬──────┘
                                                   │ MCP
                                                   ▼
                                          ┌────────────────┐
                                          │  AI assistant  │
                                          └────────────────┘
```

- **`internal/parser`** — tree-sitter bindings for each supported language.
- **`internal/indexer`** — full and incremental indexing, file hashing, fsnotify watcher.
- **`internal/db`** — SQLite schema, separate read/write handles.
- **`internal/query`** — the queries that back each MCP tool.

## Building from source

```bash
git clone https://github.com/254binaryninja/graphindex.git
cd graphindex
go build ./...
go test ./...
```

## License

[MIT](LICENSE)
