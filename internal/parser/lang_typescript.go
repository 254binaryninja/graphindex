package parser

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

func init() {
	ts := &tsLang{lang: typescript.GetLanguage()}
	RegisterLanguage(".ts", ts)
	RegisterLanguage(".tsx", &tsLang{lang: tsx.GetLanguage()})
	RegisterLanguage(".js", ts)
	RegisterLanguage(".jsx", &tsLang{lang: tsx.GetLanguage()})
}

type tsLang struct {
	lang *sitter.Language
}

func (t *tsLang) GetLanguage() *sitter.Language {
	return t.lang
}

func (t *tsLang) Extract(root *sitter.Node, content []byte) ([]Symbol, []Edge) {
	var symbols []Symbol
	var edges []Edge

	// First pass: build alias map (local name → original name)
	aliases := tsCollectAliases(root, content)

	tsWalkTopLevel(root, content, &symbols, &edges, aliases)

	// Resolve aliases in edges: if a call targets a local alias, rewrite to original
	for i := range edges {
		if original, ok := aliases[edges[i].ToSymbol]; ok {
			edges[i].ToSymbol = original
		}
	}

	return symbols, edges
}

// tsCollectAliases scans import statements for aliased imports
// e.g., `import { useAuth as auth }` → aliases["auth"] = "useAuth"
func tsCollectAliases(root *sitter.Node, content []byte) map[string]string {
	aliases := make(map[string]string)
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		if child.Type() != "import_statement" {
			continue
		}
		for j := 0; j < int(child.NamedChildCount()); j++ {
			clause := child.NamedChild(j)
			if clause.Type() != "import_clause" {
				continue
			}
			for k := 0; k < int(clause.NamedChildCount()); k++ {
				spec := clause.NamedChild(k)
				if spec.Type() != "named_imports" {
					continue
				}
				for m := 0; m < int(spec.NamedChildCount()); m++ {
					imp := spec.NamedChild(m)
					if imp.Type() != "import_specifier" {
						continue
					}
					nameNode := imp.ChildByFieldName("name")
					aliasNode := imp.ChildByFieldName("alias")
					if nameNode != nil && aliasNode != nil {
						original := nameNode.Content(content)
						local := aliasNode.Content(content)
						aliases[local] = original
					}
				}
			}
		}
	}
	return aliases
}

func tsWalkTopLevel(node *sitter.Node, content []byte, symbols *[]Symbol, edges *[]Edge, aliases map[string]string) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "function_declaration":
			if sym := tsExtractFunction(child, content); sym != nil {
				*symbols = append(*symbols, *sym)
				*edges = append(*edges, tsExtractCalls(sym.Name, child, content)...)
			}
		case "export_statement":
			tsWalkExport(child, content, symbols, edges, aliases)
		case "class_declaration":
			tsExtractClass(child, content, symbols, edges)
		case "interface_declaration":
			if sym := tsExtractInterface(child, content); sym != nil {
				*symbols = append(*symbols, *sym)
			}
		case "type_alias_declaration":
			if sym := tsExtractTypeAlias(child, content); sym != nil {
				*symbols = append(*symbols, *sym)
			}
		case "lexical_declaration":
			tsExtractLexical(child, content, symbols, edges)
		case "variable_declaration":
			tsExtractLexical(child, content, symbols, edges)
		case "import_statement":
			tsExtractImport(child, content, edges)
		}
	}
}

func tsWalkExport(node *sitter.Node, content []byte, symbols *[]Symbol, edges *[]Edge, aliases map[string]string) {
	// Check for re-export: `export { X } from './module'`
	source := node.ChildByFieldName("source")
	if source != nil {
		tsExtractReExport(node, content, symbols, edges)
		return
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "function_declaration":
			if sym := tsExtractFunction(child, content); sym != nil {
				*symbols = append(*symbols, *sym)
				*edges = append(*edges, tsExtractCalls(sym.Name, child, content)...)
			}
		case "class_declaration":
			tsExtractClass(child, content, symbols, edges)
		case "interface_declaration":
			if sym := tsExtractInterface(child, content); sym != nil {
				*symbols = append(*symbols, *sym)
			}
		case "type_alias_declaration":
			if sym := tsExtractTypeAlias(child, content); sym != nil {
				*symbols = append(*symbols, *sym)
			}
		case "lexical_declaration":
			tsExtractLexical(child, content, symbols, edges)
		case "variable_declaration":
			tsExtractLexical(child, content, symbols, edges)
		case "decorator":
			// Skip — handled when we reach the class_declaration
		}
	}
}

// tsExtractReExport handles `export { X, Y as Z } from './module'`
// Creates a symbol for the re-export that acts as a proxy edge
func tsExtractReExport(node *sitter.Node, content []byte, symbols *[]Symbol, edges *[]Edge) {
	source := node.ChildByFieldName("source")
	if source == nil {
		return
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "export_clause" {
			for j := 0; j < int(child.NamedChildCount()); j++ {
				spec := child.NamedChild(j)
				if spec.Type() != "export_specifier" {
					continue
				}
				nameNode := spec.ChildByFieldName("name")
				aliasNode := spec.ChildByFieldName("alias")

				if nameNode != nil {
					originalName := nameNode.Content(content)
					exportedName := originalName
					if aliasNode != nil {
						exportedName = aliasNode.Content(content)
					}

					// Create a symbol for the re-export
					*symbols = append(*symbols, Symbol{
						Name:      exportedName,
						Kind:      "variable",
						LineStart: int(node.StartPoint().Row) + 1,
						LineEnd:   int(node.EndPoint().Row) + 1,
					})

					// Create edge: re-exported name calls original
					*edges = append(*edges, Edge{
						FromSymbol: exportedName,
						ToSymbol:   originalName,
						Kind:       "calls",
					})
				}
			}
		}
	}
}

func tsExtractFunction(node *sitter.Node, content []byte) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      nameNode.Content(content),
		Kind:      "function",
		LineStart: int(node.StartPoint().Row) + 1,
		LineEnd:   int(node.EndPoint().Row) + 1,
		Signature: tsSignature(node, content),
	}
}

func tsExtractClass(node *sitter.Node, content []byte, symbols *[]Symbol, edges *[]Edge) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	className := nameNode.Content(content)
	kind := "class"
	if angularKind := AngularDecoratorKind(node, content); angularKind != "" {
		kind = angularKind
	}
	*symbols = append(*symbols, Symbol{
		Name:      className,
		Kind:      kind,
		LineStart: int(node.StartPoint().Row) + 1,
		LineEnd:   int(node.EndPoint().Row) + 1,
		Signature: tsSignature(node, content),
	})

	// Extract Angular constructor injection
	*edges = append(*edges, ExtractAngularDeps(className, node, content)...)

	// Extract heritage (extends/implements)
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "class_heritage" {
			for j := 0; j < int(child.NamedChildCount()); j++ {
				clause := child.NamedChild(j)
				if clause.Type() == "extends_clause" {
					tsExtractHeritage(className, clause, content, "extends", edges)
				} else if clause.Type() == "implements_clause" {
					tsExtractHeritage(className, clause, content, "implements", edges)
				}
			}
		}
	}

	// Extract methods
	body := node.ChildByFieldName("body")
	if body == nil {
		return
	}
	for i := 0; i < int(body.NamedChildCount()); i++ {
		member := body.NamedChild(i)
		if member.Type() == "method_definition" || member.Type() == "public_field_definition" {
			mName := member.ChildByFieldName("name")
			if mName != nil {
				methodName := className + "." + mName.Content(content)
				*symbols = append(*symbols, Symbol{
					Name:      methodName,
					Kind:      "function",
					LineStart: int(member.StartPoint().Row) + 1,
					LineEnd:   int(member.EndPoint().Row) + 1,
					Signature: tsSignature(member, content),
				})
				*edges = append(*edges, tsExtractCalls(methodName, member, content)...)
			}
		}
	}
}

func tsExtractHeritage(className string, clause *sitter.Node, content []byte, kind string, edges *[]Edge) {
	for i := 0; i < int(clause.NamedChildCount()); i++ {
		child := clause.NamedChild(i)
		name := child.Content(content)
		// Strip generics
		if idx := strings.Index(name, "<"); idx > 0 {
			name = name[:idx]
		}
		if name != "" {
			*edges = append(*edges, Edge{
				FromSymbol: className,
				ToSymbol:   name,
				Kind:       kind,
			})
		}
	}
}

func tsExtractInterface(node *sitter.Node, content []byte) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      nameNode.Content(content),
		Kind:      "interface",
		LineStart: int(node.StartPoint().Row) + 1,
		LineEnd:   int(node.EndPoint().Row) + 1,
		Signature: tsSignature(node, content),
	}
}

func tsExtractTypeAlias(node *sitter.Node, content []byte) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      nameNode.Content(content),
		Kind:      "type",
		LineStart: int(node.StartPoint().Row) + 1,
		LineEnd:   int(node.EndPoint().Row) + 1,
		Signature: tsSignature(node, content),
	}
}

func tsExtractLexical(node *sitter.Node, content []byte, symbols *[]Symbol, edges *[]Edge) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		decl := node.NamedChild(i)
		if decl.Type() != "variable_declarator" {
			continue
		}
		nameNode := decl.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := nameNode.Content(content)

		// Check if it's an arrow function or function expression
		value := decl.ChildByFieldName("value")
		kind := "variable"
		if value != nil && (value.Type() == "arrow_function" || value.Type() == "function_expression" || value.Type() == "function") {
			kind = "function"
			*edges = append(*edges, tsExtractCalls(name, value, content)...)
		}

		*symbols = append(*symbols, Symbol{
			Name:      name,
			Kind:      kind,
			LineStart: int(node.StartPoint().Row) + 1,
			LineEnd:   int(node.EndPoint().Row) + 1,
			Signature: tsSignature(node, content),
		})
	}
}

func tsExtractImport(node *sitter.Node, content []byte, edges *[]Edge) {
	source := node.ChildByFieldName("source")
	if source == nil {
		return
	}
	modulePath := strings.Trim(source.Content(content), "\"'`")

	// Find imported names
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "import_clause" {
			for j := 0; j < int(child.NamedChildCount()); j++ {
				spec := child.NamedChild(j)
				switch spec.Type() {
				case "identifier":
					*edges = append(*edges, Edge{
						FromSymbol: spec.Content(content),
						ToSymbol:   modulePath,
						Kind:       "imports",
					})
				case "named_imports":
					for k := 0; k < int(spec.NamedChildCount()); k++ {
						imp := spec.NamedChild(k)
						if imp.Type() == "import_specifier" {
							nameNode := imp.ChildByFieldName("name")
							if nameNode != nil {
								*edges = append(*edges, Edge{
									FromSymbol: nameNode.Content(content),
									ToSymbol:   modulePath,
									Kind:       "imports",
								})
							}
						}
					}
				}
			}
		}
	}
}

func tsExtractCalls(fromName string, node *sitter.Node, content []byte) []Edge {
	var edges []Edge
	seen := make(map[string]bool)
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Type() {
		case "call_expression":
			fnNode := n.ChildByFieldName("function")
			if fnNode != nil {
				callee := fnNode.Content(content)
				// For member access like `this.auth.login()` or `service.doThing()`,
				// also record the final method name as a separate edge
				resolved := tsResolveCallee(callee)
				if resolved != "" && resolved != fromName && !seen[resolved] {
					seen[resolved] = true
					edges = append(edges, Edge{
						FromSymbol: fromName,
						ToSymbol:   resolved,
						Kind:       "calls",
					})
				}
				// Also store the full qualified name if different
				if callee != resolved && callee != "" && callee != fromName && !seen[callee] {
					seen[callee] = true
					edges = append(edges, Edge{
						FromSymbol: fromName,
						ToSymbol:   callee,
						Kind:       "calls",
					})
				}
			}
		case "jsx_element", "jsx_self_closing_element":
			name := tsJSXComponentName(n, content)
			if name != "" && name != fromName && !seen[name] {
				// Uppercase = component (lowercase = HTML element)
				if name[0] >= 'A' && name[0] <= 'Z' {
					seen[name] = true
					edges = append(edges, Edge{
						FromSymbol: fromName,
						ToSymbol:   name,
						Kind:       "calls",
					})
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(node)
	return edges
}

func tsJSXComponentName(node *sitter.Node, content []byte) string {
	// For jsx_self_closing_element, the name is directly accessible
	nameNode := node.ChildByFieldName("name")
	if nameNode != nil {
		return nameNode.Content(content)
	}
	// For jsx_element, look at the opening_element child
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "jsx_opening_element" {
			n := child.ChildByFieldName("name")
			if n != nil {
				return n.Content(content)
			}
		}
	}
	return ""
}

// tsResolveCallee extracts the method name from a qualified call.
// "this.auth.login" → "login"
// "service.doThing" → "doThing"
// "useAuth" → "useAuth" (no change for simple names)
func tsResolveCallee(callee string) string {
	if callee == "" {
		return ""
	}
	// Strip leading "this." chains
	for strings.HasPrefix(callee, "this.") {
		callee = callee[5:]
	}
	// Get final segment after last dot
	if idx := strings.LastIndex(callee, "."); idx >= 0 {
		return callee[idx+1:]
	}
	return callee
}

func tsSignature(node *sitter.Node, content []byte) string {
	// Return first line or up to opening brace
	start := node.StartByte()
	end := node.EndByte()
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "statement_block" || child.Type() == "class_body" {
			end = child.StartByte()
			break
		}
	}
	if end-start > 200 {
		end = start + 200
	}
	sig := string(content[start:end])
	// Trim trailing whitespace
	return strings.TrimRight(sig, " \t\n\r")
}
