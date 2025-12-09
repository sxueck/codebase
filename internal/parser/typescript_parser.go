package parser

import (
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
)

// TypeScriptParser implements LanguageParser for TypeScript language
type TypeScriptParser struct{}

// NewTypeScriptParser creates a new TypeScript parser
func NewTypeScriptParser() *TypeScriptParser {
	return &TypeScriptParser{}
}

// Language returns the language name
func (p *TypeScriptParser) Language() string {
	return string(LanguageTypeScript)
}

// ExtractFunctions extracts function, method, and arrow function definitions from TypeScript source code
func (p *TypeScriptParser) ExtractFunctions(filePath string, code []byte) ([]FunctionNode, error) {
	parser := sitter.NewParser()
	parser.SetLanguage(tsx.GetLanguage())

	tree, err := parser.ParseCtx(nil, nil, code)
	if err != nil {
		return nil, fmt.Errorf("failed to parse TypeScript code: %w", err)
	}
	defer tree.Close()

	root := tree.RootNode()
	var functions []FunctionNode

	// Traverse the AST to find function definitions
	p.traverseNode(root, code, filePath, "", &functions)

	return functions, nil
}

func (p *TypeScriptParser) traverseNode(node *sitter.Node, code []byte, filePath string, className string, functions *[]FunctionNode) {
	nodeType := node.Type()

	// Handle various function declaration types
	switch nodeType {
	case "function_declaration":
		funcNode := p.extractNamedFunction(node, code, filePath, className)
		if funcNode != nil && funcNode.EndLine-funcNode.StartLine >= 2 {
			*functions = append(*functions, *funcNode)
		}

	case "method_definition":
		funcNode := p.extractMethod(node, code, filePath, className)
		if funcNode != nil && funcNode.EndLine-funcNode.StartLine >= 2 {
			*functions = append(*functions, *funcNode)
		}

	case "lexical_declaration", "variable_declaration":
		// Handle arrow functions and function expressions assigned to variables
		p.extractVariableFunctions(node, code, filePath, className, functions)

	case "class_declaration":
		// Track class context for methods
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

	case "interface_declaration", "type_alias_declaration":
		// TypeScript-specific: we could choose to include or skip these
		// For now, we'll skip them and only focus on executable code
		return
	}

	// Recursively traverse child nodes
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		p.traverseNode(child, code, filePath, className, functions)
	}
}

func (p *TypeScriptParser) extractNamedFunction(node *sitter.Node, code []byte, filePath string, className string) *FunctionNode {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	name := nameNode.Content(code)
	startPoint := node.StartPoint()
	endPoint := node.EndPoint()

	return &FunctionNode{
		Name:      name,
		NodeType:  "function",
		StartLine: int(startPoint.Row) + 1,
		EndLine:   int(endPoint.Row) + 1,
		Content:   string(code[node.StartByte():node.EndByte()]),
		StartByte: int(node.StartByte()),
		EndByte:   int(node.EndByte()),
	}
}

func (p *TypeScriptParser) extractMethod(node *sitter.Node, code []byte, filePath string, className string) *FunctionNode {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	name := nameNode.Content(code)
	if className != "" {
		name = fmt.Sprintf("%s.%s", className, name)
	}

	startPoint := node.StartPoint()
	endPoint := node.EndPoint()

	return &FunctionNode{
		Name:      name,
		NodeType:  "method",
		StartLine: int(startPoint.Row) + 1,
		EndLine:   int(endPoint.Row) + 1,
		Content:   string(code[node.StartByte():node.EndByte()]),
		StartByte: int(node.StartByte()),
		EndByte:   int(node.EndByte()),
	}
}

func (p *TypeScriptParser) extractVariableFunctions(node *sitter.Node, code []byte, filePath string, className string, functions *[]FunctionNode) {
	// Look for variable declarators
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "variable_declarator" {
			nameNode := child.ChildByFieldName("name")
			valueNode := child.ChildByFieldName("value")

			if nameNode != nil && valueNode != nil {
				valueType := valueNode.Type()
				// Check if value is an arrow function or function expression
				if valueType == "arrow_function" || valueType == "function" || valueType == "function_expression" {
					name := nameNode.Content(code)
					startPoint := valueNode.StartPoint()
					endPoint := valueNode.EndPoint()

					// Only include if it's substantial enough
					if int(endPoint.Row)-int(startPoint.Row) >= 2 {
						*functions = append(*functions, FunctionNode{
							Name:      name,
							NodeType:  "function",
							StartLine: int(startPoint.Row) + 1,
							EndLine:   int(endPoint.Row) + 1,
							Content:   string(code[valueNode.StartByte():valueNode.EndByte()]),
							StartByte: int(valueNode.StartByte()),
							EndByte:   int(valueNode.EndByte()),
						})
					}
				}
			}
		}
	}
}
