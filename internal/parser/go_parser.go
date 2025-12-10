package parser

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"go/types"
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
	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, filePath, code, goparser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Go code: %w", err)
	}

	var functions []FunctionNode
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		funcNode := p.buildFunctionNode(decl, code, fset)
		if funcNode != nil && funcNode.EndLine-funcNode.StartLine >= 2 {
			functions = append(functions, *funcNode)
		}

		return true
	})

	return functions, nil
}

func (p *GoParser) buildFunctionNode(decl *ast.FuncDecl, code []byte, fset *token.FileSet) *FunctionNode {
	if decl == nil || decl.Name == nil {
		return nil
	}

	startPos := fset.PositionFor(decl.Pos(), false)
	endPos := fset.PositionFor(decl.End(), false)
	if startPos.Offset < 0 || endPos.Offset > len(code) || endPos.Offset <= startPos.Offset {
		return nil
	}

	nodeType := "function_declaration"
	name := decl.Name.Name

	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		nodeType = "method_declaration"
		recvType := formatReceiverType(decl.Recv.List[0].Type)
		if recvType != "" {
			name = fmt.Sprintf("(%s).%s", recvType, name)
		}
	}

	content := string(code[startPos.Offset:endPos.Offset])

	return &FunctionNode{
		Name:      name,
		NodeType:  nodeType,
		StartLine: startPos.Line,
		EndLine:   endPos.Line,
		Content:   content,
		StartByte: startPos.Offset,
		EndByte:   endPos.Offset,
	}
}

func formatReceiverType(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	return types.ExprString(expr)
}
