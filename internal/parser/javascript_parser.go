package parser

// JavaScriptParser implements LanguageParser for JavaScript language
type JavaScriptParser struct{}

// NewJavaScriptParser creates a new JavaScript parser
func NewJavaScriptParser() *JavaScriptParser {
	return &JavaScriptParser{}
}

// Language returns the language name
func (p *JavaScriptParser) Language() string {
	return string(LanguageJavaScript)
}

// ExtractFunctions extracts function, method, and arrow function definitions from JavaScript source code
func (p *JavaScriptParser) ExtractFunctions(filePath string, code []byte) ([]FunctionNode, error) {
	functions := extractJSFunctions(code, false)
	return functions, nil
}
