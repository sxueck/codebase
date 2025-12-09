package parser

import "codebase/internal/models"

type LanguageParser interface {
	ExtractFunctions(path string, code []byte) ([]models.FunctionNode, error)
}
