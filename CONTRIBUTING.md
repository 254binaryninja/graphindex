# Contributing to graphindex

Thanks for your interest. This document covers how to get a dev environment running, the conventions the project follows, and what makes a PR easy to review.

## Prerequisites

- Go 1.26 or newer (`go version`)
- A C toolchain — `go-tree-sitter` uses cgo
  - macOS: `xcode-select --install`
  - Linux: `build-essential` (Debian/Ubuntu) or equivalent
- `git`

## Getting started

```bash
git clone https://github.com/254binaryninja/graphindex.git
cd graphindex
go mod download
go build ./...
go test ./...
```

To try the binary against a real repo:

```bash
go run . --repo /path/to/some/repo
```

It will speak MCP over stdio, so for quick smoke tests pipe a JSON-RPC request in or wire it up to an MCP client (Claude Code, MCP Inspector, etc.).

## Project layout

```
.
├── main.go              # MCP server entrypoint, tool registration
└── internal/
    ├── parser/          # tree-sitter bindings, one file per language
    ├── indexer/         # full + incremental indexing, fsnotify watcher
    ├── db/              # SQLite schema and read/write handles
    └── query/           # the queries behind each MCP tool
```

Everything under `internal/` is intentionally private — it cannot be imported by other modules. If you have a strong reason to expose something, open an issue first to discuss the API surface.

## Adding a language

Most contributions will fall here. To add support for a new language:

1. Add a tree-sitter grammar binding under `internal/parser/lang_<name>.go`. Look at `lang_go.go` and `lang_typescript.go` as references.
2. Register the file extensions and parser in `internal/parser/treesitter.go`.
3. Make sure your parser emits the same symbol kinds the rest of the pipeline expects (`function`, `class`, `variable`, `interface`, `type`).
4. Add a small fixture under a test, and assert symbols/edges are extracted as expected.

Keep the per-language file self-contained — node-type names and queries should not leak into shared code.

## Coding conventions

- **`gofmt` / `goimports` clean.** CI will reject anything that isn't.
- **`go vet ./...` clean.**
- **Errors get wrapped with context** (`fmt.Errorf("indexing %s: %w", path, err)`), not swallowed or logged-and-returned.
- **No new top-level packages** without discussion. Prefer extending an existing package.
- **Comments only for the non-obvious** — invariants, workarounds, surprising behavior. If a name explains itself, leave it alone.
- **No third-party deps** without justification. The dependency list is deliberately small; each addition is a supply-chain surface.

## Tests

- Unit tests live next to the code they test (`foo.go` ↔ `foo_test.go`).
- Tests must not require a network or a specific working directory — use `t.TempDir()` and table-driven tests.
- Run the full suite with `go test ./...` before opening a PR.

## Commit messages

Short, imperative subject line; body explains *why* if it isn't obvious from the diff.

```
indexer: skip vendored directories during full index

Indexing node_modules and vendor/ blew up the database on large
JS monorepos and added no useful symbols. Skip them by default;
expose an opt-in flag if anyone needs the old behavior.
```

Reference issues with `Fixes #N` or `Refs #N` when relevant.

## Pull requests

Before opening a PR:

- [ ] `go build ./...` succeeds
- [ ] `go test ./...` passes
- [ ] `go vet ./...` is clean
- [ ] `gofmt -l .` prints nothing
- [ ] You've described *what* changed and *why*

Keep PRs focused. A bug fix and a refactor in the same PR will be asked to split. Drive-by formatting changes in unrelated files make review harder — don't bundle them.

## Reporting bugs

Open an issue with:

- graphindex version (`graphindex --version` once we wire that up, or the commit SHA)
- Go version (`go version`)
- OS / arch
- Minimal repro: a tiny repo or file that triggers the bug, plus the exact MCP call or CLI invocation
- What you expected vs. what happened

Logs go to stderr — capture them with `2>graphindex.log` and attach.

## Security

Don't open public issues for security problems. Email the maintainer listed in `LICENSE` instead.

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE) that covers the project.
