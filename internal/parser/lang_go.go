package parser

import (
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
)

func init() {
	RegisterLanguage(".go", &goLang{})
}

type goLang struct{}

func (g *goLang) GetLanguage() *sitter.Language {
	return golang.GetLanguage()
}

func (g *goLang) Extract(root *sitter.Node, content []byte) ([]Symbol, []Edge) {
	var symbols []Symbol
	var edges []Edge

	for i := 0; i < int(root.NamedChildCount()); i++ {
		node := root.NamedChild(i)
		switch node.Type() {
		case "function_declaration":
			sym := goExtractFunction(node, content)
			if sym != nil {
				symbols = append(symbols, *sym)
				edges = append(edges, goExtractCalls(sym.Name, node, content)...)
			}
		case "method_declaration":
			sym := goExtractMethod(node, content)
			if sym != nil {
				symbols = append(symbols, *sym)
				edges = append(edges, goExtractCalls(sym.Name, node, content)...)
			}
		case "type_declaration":
			symbols = append(symbols, goExtractTypes(node, content)...)
		case "var_declaration", "const_declaration", "short_var_declaration":
			symbols = append(symbols, goExtractVars(node, content)...)
		}
	}

	return symbols, edges
}

func goExtractFunction(node *sitter.Node, content []byte) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	sig := string(content[node.StartByte():goFindSignatureEnd(node)])
	return &Symbol{
		Name:      nameNode.Content(content),
		Kind:      "function",
		LineStart: int(node.StartPoint().Row) + 1,
		LineEnd:   int(node.EndPoint().Row) + 1,
		Signature: sig,
	}
}

func goExtractMethod(node *sitter.Node, content []byte) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	sig := string(content[node.StartByte():goFindSignatureEnd(node)])
	return &Symbol{
		Name:      nameNode.Content(content),
		Kind:      "function",
		LineStart: int(node.StartPoint().Row) + 1,
		LineEnd:   int(node.EndPoint().Row) + 1,
		Signature: sig,
	}
}

func goExtractTypes(node *sitter.Node, content []byte) []Symbol {
	var symbols []Symbol
	for i := 0; i < int(node.NamedChildCount()); i++ {
		spec := node.NamedChild(i)
		if spec.Type() != "type_spec" {
			continue
		}
		nameNode := spec.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		typeNode := spec.ChildByFieldName("type")
		kind := "type"
		if typeNode != nil {
			switch typeNode.Type() {
			case "interface_type":
				kind = "interface"
			case "struct_type":
				kind = "class"
			}
		}
		symbols = append(symbols, Symbol{
			Name:      nameNode.Content(content),
			Kind:      kind,
			LineStart: int(spec.StartPoint().Row) + 1,
			LineEnd:   int(spec.EndPoint().Row) + 1,
			Signature: string(content[spec.StartByte():spec.EndByte()]),
		})
	}
	return symbols
}

func goExtractVars(node *sitter.Node, content []byte) []Symbol {
	var symbols []Symbol
	for i := 0; i < int(node.NamedChildCount()); i++ {
		spec := node.NamedChild(i)
		if spec.Type() != "var_spec" && spec.Type() != "const_spec" {
			continue
		}
		nameNode := spec.ChildByFieldName("name")
		if nameNode == nil {
			for j := 0; j < int(spec.NamedChildCount()); j++ {
				child := spec.NamedChild(j)
				if child.Type() == "identifier" {
					symbols = append(symbols, Symbol{
						Name:      child.Content(content),
						Kind:      "variable",
						LineStart: int(spec.StartPoint().Row) + 1,
						LineEnd:   int(spec.EndPoint().Row) + 1,
					})
				}
			}
			continue
		}
		symbols = append(symbols, Symbol{
			Name:      nameNode.Content(content),
			Kind:      "variable",
			LineStart: int(spec.StartPoint().Row) + 1,
			LineEnd:   int(spec.EndPoint().Row) + 1,
		})
	}
	return symbols
}

func goExtractCalls(fromName string, node *sitter.Node, content []byte) []Edge {
	var edges []Edge
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Type() == "call_expression" {
			fnNode := n.ChildByFieldName("function")
			if fnNode != nil {
				callee := fnNode.Content(content)
				if callee != "" && callee != fromName {
					edges = append(edges, Edge{
						FromSymbol: fromName,
						ToSymbol:   callee,
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

func goFindSignatureEnd(node *sitter.Node) uint32 {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "block" {
			return child.StartByte()
		}
	}
	end := node.EndByte()
	if end > node.StartByte()+200 {
		return node.StartByte() + 200
	}
	return end
}
