package parser

import (
	"context"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

type Symbol struct {
	Name      string
	Kind      string
	LineStart int
	LineEnd   int
	Signature string
}

type Edge struct {
	FromSymbol string
	ToSymbol   string
	Kind       string
}

type Language interface {
	GetLanguage() *sitter.Language
	Extract(root *sitter.Node, content []byte) ([]Symbol, []Edge)
}

var languages = map[string]Language{}

func RegisterLanguage(ext string, lang Language) {
	languages[ext] = lang
}

func Parse(path string, content []byte) ([]Symbol, []Edge) {
	ext := strings.ToLower(filepath.Ext(path))
	lang, ok := languages[ext]
	if !ok {
		return nil, nil
	}

	parser := sitter.NewParser()
	parser.SetLanguage(lang.GetLanguage())

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, nil
	}
	defer tree.Close()

	return lang.Extract(tree.RootNode(), content)
}

func SupportedExtensions() []string {
	exts := make([]string, 0, len(languages))
	for ext := range languages {
		exts = append(exts, ext)
	}
	return exts
}
