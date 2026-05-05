package parser

import (
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/html"
)

func init() {
	RegisterLanguage(".html", &angularHTMLLang{})
}

type angularHTMLLang struct{}

func (a *angularHTMLLang) GetLanguage() *sitter.Language {
	return html.GetLanguage()
}

func (a *angularHTMLLang) Extract(root *sitter.Node, content []byte) ([]Symbol, []Edge) {
	var edges []Edge
	seen := make(map[string]bool)

	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Type() == "element" || n.Type() == "self_closing_tag" {
			tag := htmlTagName(n, content)
			if tag != "" && isAngularComponent(tag) && !seen[tag] {
				seen[tag] = true
				edges = append(edges, Edge{
					FromSymbol: "_template",
					ToSymbol:   selectorToClassName(tag),
					Kind:       "calls",
				})
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)

	return nil, edges
}

func htmlTagName(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "start_tag" || child.Type() == "self_closing_tag" {
			tagName := child.ChildByFieldName("tag_name")
			if tagName != nil {
				return tagName.Content(content)
			}
			// Fallback: first child of start_tag is often the tag name
			for j := 0; j < int(child.NamedChildCount()); j++ {
				named := child.NamedChild(j)
				if named.Type() == "tag_name" {
					return named.Content(content)
				}
			}
		}
		if child.Type() == "tag_name" {
			return child.Content(content)
		}
	}
	return ""
}

// isAngularComponent checks if a tag looks like an Angular component selector
// Angular components use kebab-case with a prefix (e.g., app-header, mat-button)
func isAngularComponent(tag string) bool {
	if strings.Contains(tag, "-") {
		return true
	}
	return false
}

// selectorToClassName converts kebab-case selector to PascalCase class name
// e.g., "app-header" -> "AppHeader", "mat-button" -> "MatButton"
func selectorToClassName(selector string) string {
	parts := strings.Split(selector, "-")
	var result strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			result.WriteString(strings.ToUpper(p[:1]))
			result.WriteString(p[1:])
		}
	}
	return result.String()
}

// Angular-specific TypeScript enhancements applied during TS extraction

var decoratorPattern = regexp.MustCompile(`@(Component|Injectable|Directive|Pipe|NgModule)\b`)

// AngularDecoratorKind returns the Angular-specific kind for a decorated class
func AngularDecoratorKind(node *sitter.Node, content []byte) string {
	// Look for decorator above the class
	if node.PrevNamedSibling() != nil && node.PrevNamedSibling().Type() == "decorator" {
		decContent := node.PrevNamedSibling().Content(content)
		matches := decoratorPattern.FindStringSubmatch(decContent)
		if len(matches) > 1 {
			return strings.ToLower(matches[1])
		}
	}
	// Check parent export_statement for decorator
	return ""
}

// ExtractAngularDeps extracts constructor injection dependencies
func ExtractAngularDeps(className string, node *sitter.Node, content []byte) []Edge {
	var edges []Edge
	body := node.ChildByFieldName("body")
	if body == nil {
		return nil
	}

	for i := 0; i < int(body.NamedChildCount()); i++ {
		member := body.NamedChild(i)
		if member.Type() != "method_definition" {
			continue
		}
		nameNode := member.ChildByFieldName("name")
		if nameNode == nil || nameNode.Content(content) != "constructor" {
			continue
		}
		// Extract parameter types as dependencies
		params := member.ChildByFieldName("parameters")
		if params == nil {
			continue
		}
		for j := 0; j < int(params.NamedChildCount()); j++ {
			param := params.NamedChild(j)
			edges = append(edges, extractParamType(className, param, content)...)
		}
	}
	return edges
}

func extractParamType(className string, param *sitter.Node, content []byte) []Edge {
	var edges []Edge
	// Look for type annotation
	for i := 0; i < int(param.ChildCount()); i++ {
		child := param.Child(i)
		if child.Type() == "type_annotation" {
			for j := 0; j < int(child.NamedChildCount()); j++ {
				typeNode := child.NamedChild(j)
				typeName := typeNode.Content(content)
				// Strip generics
				if idx := strings.Index(typeName, "<"); idx > 0 {
					typeName = typeName[:idx]
				}
				if typeName != "" && typeName[0] >= 'A' && typeName[0] <= 'Z' {
					edges = append(edges, Edge{
						FromSymbol: className,
						ToSymbol:   typeName,
						Kind:       "imports",
					})
				}
			}
		}
	}
	return edges
}
