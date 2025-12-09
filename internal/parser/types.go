package parser

// FunctionNode represents a parsed function or method from source code
type FunctionNode struct {
	Name      string // Function/method name
	NodeType  string // "function", "method", "class", etc.
	StartLine int    // Starting line number (1-indexed)
	EndLine   int    // Ending line number (1-indexed)
	Content   string // Full source code of the function
	StartByte int    // Starting byte offset in file
	EndByte   int    // Ending byte offset in file
}

// LanguageParser defines the interface for language-specific parsers
type LanguageParser interface {
	// ExtractFunctions parses source code and extracts function/method definitions
	ExtractFunctions(filePath string, code []byte) ([]FunctionNode, error)

	// Language returns the language name
	Language() string
}

// Language represents supported programming languages
type Language string

const (
	LanguageGo         Language = "go"
	LanguagePython     Language = "python"
	LanguageJavaScript Language = "javascript"
	LanguageTypeScript Language = "typescript"
)
