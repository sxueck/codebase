package parser

import (
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"
)

// PythonParser implements LanguageParser for Python language
type PythonParser struct{}

// NewPythonParser creates a new Python parser
func NewPythonParser() *PythonParser {
	return &PythonParser{}
}

// Language returns the language name
func (p *PythonParser) Language() string {
	return string(LanguagePython)
}

// ExtractFunctions extracts function and class method definitions from Python source code
func (p *PythonParser) ExtractFunctions(filePath string, code []byte) ([]FunctionNode, error) {
	parser := sitter.NewParser()
	parser.SetLanguage(python.GetLanguage())

	tree, err := parser.ParseCtx(nil, nil, code)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Python code: %w", err)
	}
	defer tree.Close()

	root := tree.RootNode()
	var functions []FunctionNode

	// Traverse the AST to find function and class definitions
	p.traverseNode(root, code, filePath, "", &functions)

	return functions, nil
}

func (p *PythonParser) traverseNode(node *sitter.Node, code []byte, filePath string, className string, functions *[]FunctionNode) {
	nodeType := node.Type()

	// Check for function definitions
	if nodeType == "function_definition" {
		funcNode := p.extractFunction(node, code, filePath, className)
		if funcNode != nil {
			// Filter out very short functions
			if funcNode.EndLine-funcNode.StartLine >= 2 {
				*functions = append(*functions, *funcNode)
			}
		}
	}

	// Check for class definitions to track context
	if nodeType == "class_definition" {
		nameNode := node.ChildByFieldName("name")
		var currentClassName string
		if nameNode != nil {
			currentClassName = nameNode.Content(code)
		}

		// Recursively traverse class body with class context
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			p.traverseNode(child, code, filePath, currentClassName, functions)
		}
		return
	}

	// Recursively traverse child nodes
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		p.traverseNode(child, code, filePath, className, functions)
	}
}

func (p *PythonParser) extractFunction(node *sitter.Node, code []byte, filePath string, className string) *FunctionNode {
	startByte := node.StartByte()
	endByte := node.EndByte()
	startPoint := node.StartPoint()
	endPoint := node.EndPoint()

	// Extract function name
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	name := nameNode.Content(code)

	// Determine node type (function or method)
	nodeTypeStr := "function"
	if className != "" {
		nodeTypeStr = "method"
		name = fmt.Sprintf("%s.%s", className, name)
	}

	content := string(code[startByte:endByte])

	return &FunctionNode{
		Name:      name,
		NodeType:  nodeTypeStr,
		StartLine: int(startPoint.Row) + 1,
		EndLine:   int(endPoint.Row) + 1,
		Content:   content,
		StartByte: int(startByte),
		EndByte:   int(endByte),
	}
}
