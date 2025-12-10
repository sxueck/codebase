package parser

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

// ExtractFunctions extracts function, method, and arrow function definitions from TypeScript source code.
func (p *TypeScriptParser) ExtractFunctions(filePath string, code []byte) ([]FunctionNode, error) {
	functions := extractJSFunctions(code, true)
	return functions, nil
}
