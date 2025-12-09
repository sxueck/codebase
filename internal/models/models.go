package models

type CodeChunkPayload struct {
	FilePath  string  `json:"file_path"`
	Language  string  `json:"language"`
	NodeType  string  `json:"node_type"`
	NodeName  string  `json:"node_name"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	CodeHash  string  `json:"code_hash"`
	Content   string  `json:"content"`
}

type FunctionNode struct {
	Name      string
	NodeType  string
	StartLine int
	EndLine   int
	Content   string
}

type IntentType string

const (
	IntentSearch     IntentType = "SEARCH"
	IntentDuplicate  IntentType = "DUPLICATE"
	IntentRefactor   IntentType = "REFACTOR"
	IntentBugPattern IntentType = "BUG_PATTERN"
)

type QueryFilter struct {
	Languages  []string `json:"languages"`
	PathPrefix []string `json:"path_prefix"`
	NodeTypes  []string `json:"node_types"`
	MinLines   int      `json:"min_lines"`
	MaxLines   int      `json:"max_lines"`
}

type QueryPlan struct {
	Intent     IntentType   `json:"intent"`
	SubQueries []string     `json:"sub_queries"`
	Filter     QueryFilter  `json:"filter"`
	Threshold  float64      `json:"threshold"`
}

type DuplicateGroup struct {
	Chunks      []CodeChunkPayload `json:"chunks"`
	AvgScore    float64            `json:"avg_score"`
	Reason      string             `json:"reason"`
}

type PairCandidate struct {
	A     CodeChunkPayload
	B     CodeChunkPayload
	Score float64
}
