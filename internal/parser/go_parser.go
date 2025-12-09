package parser

import (
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
)

// GoParser implements LanguageParser for Go language
type GoParser struct{}

// NewGoParser creates a new Go parser
func NewGoParser() *GoParser {
	return &GoParser{}
}

// Language returns the language name
func (p *GoParser) Language() string {
	return string(LanguageGo)
}

// ExtractFunctions extracts function and method definitions from Go source code
func (p *GoParser) ExtractFunctions(filePath string, code []byte) ([]FunctionNode, error) {
	parser := sitter.NewParser()
	parser.SetLanguage(golang.GetLanguage())

	tree, err := parser.ParseCtx(nil, nil, code)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Go code: %w", err)
	}
	defer tree.Close()

	root := tree.RootNode()
	var functions []FunctionNode

	// Query for function declarations and method declarations
	p.traverseNode(root, code, filePath, &functions)

	return functions, nil
}

func (p *GoParser) traverseNode(node *sitter.Node, code []byte, filePath string, functions *[]FunctionNode) {
	nodeType := node.Type()

	// Check if this is a function or method declaration
	if nodeType == "function_declaration" || nodeType == "method_declaration" {
		funcNode := p.extractFunction(node, code, filePath, nodeType)
		if funcNode != nil {
			// Filter out very short functions (likely trivial getters/setters)
			if funcNode.EndLine-funcNode.StartLine >= 2 {
				*functions = append(*functions, *funcNode)
			}
		}
	}

	// Recursively traverse child nodes
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		p.traverseNode(child, code, filePath, functions)
	}
}

func (p *GoParser) extractFunction(node *sitter.Node, code []byte, filePath string, nodeType string) *FunctionNode {
	startByte := node.StartByte()
	endByte := node.EndByte()
	startPoint := node.StartPoint()
	endPoint := node.EndPoint()

	// Extract function name
	var name string
	if nodeType == "function_declaration" {
		nameNode := node.ChildByFieldName("name")
		if nameNode != nil {
			name = nameNode.Content(code)
		}
	} else if nodeType == "method_declaration" {
		nameNode := node.ChildByFieldName("name")
		if nameNode != nil {
			name = nameNode.Content(code)
		}
		// Optionally include receiver type
		receiverNode := node.ChildByFieldName("receiver")
		if receiverNode != nil {
			receiverText := receiverNode.Content(code)
			name = fmt.Sprintf("%s.%s", strings.TrimSpace(receiverText), name)
		}
	}

	content := string(code[startByte:endByte])

	return &FunctionNode{
		Name:      name,
		NodeType:  nodeType,
		StartLine: int(startPoint.Row) + 1, // tree-sitter uses 0-indexed rows
		EndLine:   int(endPoint.Row) + 1,
		Content:   content,
		StartByte: int(startByte),
		EndByte:   int(endByte),
	}
}
