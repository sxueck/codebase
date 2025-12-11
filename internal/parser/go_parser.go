package parser

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"go/types"
	"sort"
	"strings"
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

	pkgName := ""
	if file.Name != nil {
		pkgName = file.Name.Name
	}
	imports := extractImports(file)

	var functions []FunctionNode
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		funcNode := p.buildFunctionNode(decl, pkgName, imports, code, fset)
		if funcNode != nil && funcNode.EndLine-funcNode.StartLine >= 2 {
			functions = append(functions, *funcNode)
		}

		return true
	})

	return functions, nil
}

func (p *GoParser) buildFunctionNode(decl *ast.FuncDecl, pkg string, imports []string, code []byte, fset *token.FileSet) *FunctionNode {
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
	receiverType := ""
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		nodeType = "method_declaration"
		receiverType = formatReceiverType(decl.Recv.List[0].Type)
		if receiverType != "" {
			name = fmt.Sprintf("(%s).%s", receiverType, name)
		}
	}

	doc := ""
	if decl.Doc != nil {
		doc = strings.TrimSpace(decl.Doc.Text())
	}
	callees := collectCallees(decl.Body)
	content := string(code[startPos.Offset:endPos.Offset])
	signature := formatFunctionSignature(decl)

	return &FunctionNode{
		Name:        name,
		NodeType:    nodeType,
		StartLine:   startPos.Line,
		EndLine:     endPos.Line,
		Content:     content,
		StartByte:   startPos.Offset,
		EndByte:     endPos.Offset,
		PackageName: pkg,
		Imports:     append([]string(nil), imports...),
		Signature:   signature,
		Receiver:    receiverType,
		Doc:         doc,
		Callees:     callees,
	}
}

func formatReceiverType(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	return types.ExprString(expr)
}

func extractImports(file *ast.File) []string {
	if file == nil || len(file.Imports) == 0 {
		return nil
	}

	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, "\"")
		if spec.Name != nil && spec.Name.Name != "" {
			imports = append(imports, fmt.Sprintf("%s=%s", spec.Name.Name, path))
		} else {
			imports = append(imports, path)
		}
	}
	sort.Strings(imports)
	return imports
}

func formatFunctionSignature(decl *ast.FuncDecl) string {
	if decl == nil || decl.Type == nil {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("func ")
	if decl.Recv != nil {
		recv := formatFieldList(decl.Recv)
		if recv != "" {
			builder.WriteString("(")
			builder.WriteString(recv)
			builder.WriteString(") ")
		}
	}

	funcName := ""
	if decl.Name != nil {
		funcName = decl.Name.Name
	}
	builder.WriteString(funcName)
	builder.WriteString("(")
	builder.WriteString(formatFieldList(decl.Type.Params))
	builder.WriteString(")")
	builder.WriteString(formatResultSuffix(decl.Type.Results))
	return builder.String()
}

func formatFieldList(list *ast.FieldList) string {
	if list == nil || len(list.List) == 0 {
		return ""
	}

	var parts []string
	for _, field := range list.List {
		typeStr := ""
		if field.Type != nil {
			typeStr = types.ExprString(field.Type)
		}
		if len(field.Names) == 0 {
			parts = append(parts, strings.TrimSpace(typeStr))
			continue
		}
		for _, name := range field.Names {
			if typeStr != "" {
				parts = append(parts, fmt.Sprintf("%s %s", name.Name, typeStr))
			} else {
				parts = append(parts, name.Name)
			}
		}
	}
	return strings.Join(parts, ", ")
}

func formatResultSuffix(list *ast.FieldList) string {
	if list == nil || list.NumFields() == 0 {
		return ""
	}

	results := formatFieldList(list)
	if results == "" {
		return ""
	}

	if list.NumFields() == 1 && len(list.List[0].Names) == 0 {
		return " " + results
	}
	return " (" + results + ")"
}

func collectCallees(body *ast.BlockStmt) []string {
	if body == nil {
		return nil
	}

	seen := make(map[string]struct{})
	var callees []string
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := formatCallExpr(call.Fun)
		if name == "" {
			return true
		}
		if _, exists := seen[name]; exists {
			return true
		}
		seen[name] = struct{}{}
		callees = append(callees, name)
		return true
	})
	sort.Strings(callees)
	return callees
}

func formatCallExpr(expr ast.Expr) string {
	if expr == nil {
		return ""
	}

	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		prefix := formatCallExpr(e.X)
		if prefix == "" {
			return e.Sel.Name
		}
		return fmt.Sprintf("%s.%s", prefix, e.Sel.Name)
	default:
		return strings.TrimSpace(types.ExprString(expr))
	}
}
