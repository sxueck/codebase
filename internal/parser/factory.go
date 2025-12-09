package parser

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ParserFactory creates language-specific parsers
type ParserFactory struct {
	parsers map[Language]LanguageParser
}

// NewParserFactory creates a new parser factory with all supported languages
func NewParserFactory() *ParserFactory {
	return &ParserFactory{
		parsers: map[Language]LanguageParser{
			LanguageGo:         NewGoParser(),
			LanguagePython:     NewPythonParser(),
			LanguageJavaScript: NewJavaScriptParser(),
			LanguageTypeScript: NewTypeScriptParser(),
		},
	}
}

// GetParser returns a parser for the given language
func (f *ParserFactory) GetParser(lang Language) (LanguageParser, error) {
	parser, exists := f.parsers[lang]
	if !exists {
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}
	return parser, nil
}

// GetParserByFilePath returns a parser based on file extension
func (f *ParserFactory) GetParserByFilePath(filePath string) (LanguageParser, error) {
	lang := DetectLanguage(filePath)
	if lang == "" {
		return nil, fmt.Errorf("unsupported file type: %s", filePath)
	}
	return f.GetParser(lang)
}

// DetectLanguage detects the programming language based on file extension
func DetectLanguage(filePath string) Language {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".go":
		return LanguageGo
	case ".py":
		return LanguagePython
	case ".js", ".jsx", ".mjs", ".cjs":
		return LanguageJavaScript
	case ".ts", ".tsx":
		return LanguageTypeScript
	default:
		return ""
	}
}

// SupportedExtensions returns all supported file extensions
func SupportedExtensions() []string {
	return []string{
		".go",
		".py",
		".js", ".jsx", ".mjs", ".cjs",
		".ts", ".tsx",
	}
}

// IsSupportedFile checks if a file is supported based on its extension
func IsSupportedFile(filePath string) bool {
	return DetectLanguage(filePath) != ""
}
