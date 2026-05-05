package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/arnoldmusandu/graphindex/internal/db"
	"github.com/arnoldmusandu/graphindex/internal/indexer"
	"github.com/arnoldmusandu/graphindex/internal/query"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	log.SetOutput(os.Stderr)

	repo := flag.String("repo", ".", "Path to the repository root to index")
	dbPath := flag.String("db", "", "Path to SQLite database file (default: <repo>/.graphindex.db)")
	flag.Parse()

	absRepo, err := filepath.Abs(*repo)
	if err != nil {
		log.Fatalf("invalid repo path: %v", err)
	}

	if *dbPath == "" {
		*dbPath = filepath.Join(absRepo, ".graphindex.db")
	}

	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	idx := indexer.New(database, absRepo)

	go func() {
		log.Println("starting initial index...")
		if err := idx.IndexRepo(); err != nil {
			log.Printf("index error: %v", err)
		}
		log.Println("initial index complete")
	}()

	go func() {
		if err := idx.Watch(); err != nil {
			log.Printf("watcher error: %v", err)
		}
	}()

	s := server.NewMCPServer("GraphIndex", "0.1.0",
		server.WithToolCapabilities(true),
	)

	s.AddTool(mcp.Tool{
		Name:        "search_symbols",
		Description: "Find functions, classes, variables, and types by name. Use before reading a file to confirm the symbol exists and get its exact location. Prefer this over grep for symbol lookup.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Symbol name or partial name",
				},
				"kind": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"function", "class", "variable", "interface", "type", "any"},
					"description": "Filter by symbol kind. Omit or use 'any' for all kinds.",
				},
			},
			Required: []string{"query"},
		},
	}, handleSearchSymbols(database))

	s.AddTool(mcp.Tool{
		Name:        "get_definition",
		Description: "Return the source location and signature for a symbol. Use when you know the exact symbol name and need its file path and line range.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Exact symbol name",
				},
			},
			Required: []string{"name"},
		},
	}, handleGetDefinition(database))

	s.AddTool(mcp.Tool{
		Name:        "analyze_impact",
		Description: "Map what breaks if a symbol changes. Returns direct callers and transitive dependents up to depth 3. Use before refactoring to assess blast radius.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"symbol": map[string]interface{}{
					"type":        "string",
					"description": "Symbol name to analyze",
				},
				"max_depth": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum traversal depth (1-4, default 3)",
				},
			},
			Required: []string{"symbol"},
		},
	}, handleAnalyzeImpact(database))

	s.AddTool(mcp.Tool{
		Name:        "get_module_summary",
		Description: "Get high-level stats for a file: symbol count, function count, dependency count, and last index time. Use to understand file complexity at a glance.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"file": map[string]interface{}{
					"type":        "string",
					"description": "File path (as indexed)",
				},
			},
			Required: []string{"file"},
		},
	}, handleGetModuleSummary(database))

	s.AddTool(mcp.Tool{
		Name:        "explore_symbol",
		Description: "All-in-one symbol exploration: returns definition, signature, direct callers, impact assessment, and module stats in a single call. Use this instead of calling search_symbols + get_definition + analyze_impact separately.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Exact symbol name to explore",
				},
			},
			Required: []string{"name"},
		},
	}, handleExploreSymbol(database))

	if err := server.ServeStdio(s); err != nil {
		log.Fatal(err)
	}
}

func handleSearchSymbols(database *db.DB) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		q, _ := args["query"].(string)
		kind, _ := args["kind"].(string)

		result, err := query.SearchSymbols(database.Read, q, kind)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleGetDefinition(database *db.DB) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		name, _ := args["name"].(string)

		result, err := query.GetDefinition(database.Read, name)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleAnalyzeImpact(database *db.DB) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		symbol, _ := args["symbol"].(string)
		maxDepth := 3
		if d, ok := args["max_depth"].(float64); ok {
			maxDepth = int(d)
		}

		result, err := query.AnalyzeImpact(database.Read, symbol, maxDepth)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleGetModuleSummary(database *db.DB) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		file, _ := args["file"].(string)

		result, err := query.GetModuleSummary(database.Read, file)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleExploreSymbol(database *db.DB) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		name, _ := args["name"].(string)

		result, err := query.ExploreSymbol(database.Read, name)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}
}
