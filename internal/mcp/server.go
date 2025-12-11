package mcp

import (
	"bufio"
	"codebase/internal/analyzer"
	"codebase/internal/config"
	"codebase/internal/embeddings"
	"codebase/internal/indexer"
	"codebase/internal/llm"
	"codebase/internal/models"
	"codebase/internal/qdrant"
	"codebase/internal/utils"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// SearchResult holds a code chunk with its fusion score for multi-query recall
type SearchResult struct {
	models.CodeChunkPayload
	FusionScore float64
	Rank        int
}

// QueryPlan alias for models.QueryPlan
type QueryPlan = models.QueryPlan

// CodeChunkPayload alias for models.CodeChunkPayload
type CodeChunkPayload = models.CodeChunkPayload

type Server struct {
	qdrantClient *qdrant.Client
	embedClient  *embeddings.Client
	llmClient    *llm.Client
	analyzer     *analyzer.Analyzer
	collection   string
}

func (s *Server) collectionName() string {
	if strings.TrimSpace(s.collection) == "" {
		return indexer.CollectionName("")
	}
	return s.collection
}

func NewServer(rootDir string) (*Server, error) {
	// Best-effort load of shared config from ~/.codebase/config.json.
	// Values from this file are applied as environment variables (if not
	// already set) so that downstream clients can use the standard config
	// layer based on env vars.
	if err := config.LoadFromUserConfig(); err != nil {
		return nil, err
	}

	qc, err := qdrant.NewClient()
	if err != nil {
		return nil, err
	}

	projectID, err := utils.ComputeProjectID(rootDir)
	if err != nil {
		return nil, err
	}
	collection := indexer.CollectionName(projectID)

	ec := embeddings.NewClient()
	lc := llm.NewClient()
	az := analyzer.NewAnalyzer(qc, lc, collection)

	return &Server{
		qdrantClient: qc,
		embedClient:  ec,
		llmClient:    lc,
		analyzer:     az,
		collection:   collection,
	}, nil
}

func (s *Server) Run() error {
	defer s.qdrantClient.Close()

	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	for {
		payload, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			s.writeError(writer, nil, -32700, err.Error())
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			s.writeError(writer, nil, -32700, "Parse error")
			continue
		}

		s.handleRequest(writer, &req)
	}

	return nil
}

func (s *Server) handleRequest(writer *bufio.Writer, req *JSONRPCRequest) {
	switch req.Method {
	case "initialize":
		s.handleInitialize(writer, req)
	case "tools/list":
		s.handleToolsList(writer, req)
	case "tools/call":
		s.handleToolsCall(writer, req)
	case "resources/list":
		s.handleResourcesList(writer, req)
	case "prompts/list":
		s.handlePromptsList(writer, req)
	case "ping":
		s.handlePing(writer, req)
	case "shutdown":
		s.writeResponse(writer, req.ID, map[string]interface{}{})
	case "notifications/initialized":
		return
	case "exit":
		os.Exit(0)
	default:
		if req.ID != nil {
			s.writeError(writer, req.ID, -32601, "Method not found")
		}
	}
}

func (s *Server) handleInitialize(writer *bufio.Writer, req *JSONRPCRequest) {
	result := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"serverInfo": map[string]string{
			"name":    "codebase-mcp",
			"version": "1.0.0",
		},
		"capabilities": map[string]interface{}{
			"tools":     map[string]interface{}{},
			"resources": map[string]interface{}{},
			"prompts":   map[string]interface{}{},
		},
	}
	s.writeResponse(writer, req.ID, result)
}

func (s *Server) handleResourcesList(writer *bufio.Writer, req *JSONRPCRequest) {
	s.writeResponse(writer, req.ID, map[string]interface{}{
		"resources": []interface{}{},
	})
}

func (s *Server) handlePromptsList(writer *bufio.Writer, req *JSONRPCRequest) {
	s.writeResponse(writer, req.ID, map[string]interface{}{
		"prompts": []interface{}{},
	})
}

func (s *Server) handlePing(writer *bufio.Writer, req *JSONRPCRequest) {
	s.writeResponse(writer, req.ID, map[string]interface{}{
		"status": "ok",
	})
}

func (s *Server) handleToolsList(writer *bufio.Writer, req *JSONRPCRequest) {
	tools := []map[string]interface{}{
		{
			"name": "codebase-retrieval",
			"description": "Primary MCP for this repo. Before attempting ANY code retrieval, requirement analysis, or contextual grounding for a user request, you must call this tool once. It performs semantic code search over the indexed repository so answers stay anchored to real code instead of assumptions.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type": "string",
						"description": "Natural language description of what you need to inspect in this repo. Always populate this and call the tool first whenever (a) a user asks for code search, diffing, or refactors, or (b) you need to understand user requirements by locating related context in the codebase. Include specific symbols/files/features to maximize recall.",
					},
					"top_k": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of code snippets to return (default 10). Increase this when you need broader coverage of the relevant implementation (for example, 20–50 when exploring a feature area) and lower it when you only need the single most relevant location.",
					},
				},
				"required": []string{"query"},
			},
		},
	}
	s.writeResponse(writer, req.ID, map[string]interface{}{"tools": tools})
}

func (s *Server) handleToolsCall(writer *bufio.Writer, req *JSONRPCRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}

	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.writeError(writer, req.ID, -32602, "Invalid params")
		return
	}

	var result interface{}
	var err error

	switch params.Name {
	case "codebase-retrieval":
		result, err = s.handleCodebaseRetrieval(params.Arguments)
	default:
		s.writeError(writer, req.ID, -32602, "Unknown tool")
		return
	}

	if err != nil {
		s.writeError(writer, req.ID, -32603, err.Error())
		return
	}

	s.writeResponse(writer, req.ID, map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": formatResult(result),
			},
		},
	})
}

func (s *Server) handleCodebaseRetrieval(args json.RawMessage) (interface{}, error) {
	return s.handleSearchCode(args)
}

func (s *Server) handleSearchCode(args json.RawMessage) (interface{}, error) {
	var input struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}

	if input.TopK == 0 {
		input.TopK = 10
	}

	// Step 1: Use LLM to build QueryPlan (intent recognition and query rewriting)
	plan, err := s.llmClient.BuildQueryPlan(input.Query)
	if err != nil {
		// Fallback to simple search if LLM fails
		return s.simpleSearch(input.Query, input.TopK)
	}

	// Step 2: Branch based on intent type
	switch plan.Intent {
	case "DUPLICATE":
		return s.handleDuplicateIntent(plan)
	case "SEARCH", "REFACTOR", "BUG_PATTERN":
		return s.handleSemanticIntent(plan, input.TopK)
	default:
		// Unknown intent, fallback to simple search
		return s.simpleSearch(input.Query, input.TopK)
	}
}

// simpleSearch performs basic semantic search without query planning
func (s *Server) simpleSearch(query string, topK int) (interface{}, error) {
	vec, err := s.embedClient.Embed(query)
	if err != nil {
		return nil, err
	}

	results, err := s.qdrantClient.Search(s.collectionName(), vec, uint64(topK))
	if err != nil {
		return nil, err
	}

	return results, nil
}

// handleDuplicateIntent processes duplicate detection queries
func (s *Server) handleDuplicateIntent(plan *QueryPlan) (interface{}, error) {
	duplicates, err := s.analyzer.FindDuplicates(*plan)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"intent":     "DUPLICATE",
		"duplicates": duplicates,
	}, nil
}

// handleSemanticIntent processes semantic search with multi-query recall and fusion
func (s *Server) handleSemanticIntent(plan *QueryPlan, topK int) (interface{}, error) {
	if len(plan.SubQueries) == 0 {
		return nil, fmt.Errorf("no sub-queries in plan")
	}

	// Multi-query recall: embed and search each sub-query
	allResults := make(map[string]*SearchResult)

	for _, subQuery := range plan.SubQueries {
		vec, err := s.embedClient.Embed(subQuery)
		if err != nil {
			continue // Skip failed embeddings
		}

		scoredPoints, err := s.qdrantClient.Search(s.collectionName(), vec, uint64(topK*2))
		if err != nil {
			continue // Skip failed searches
		}

		// Merge results with reciprocal rank fusion
		for _, point := range scoredPoints {
			// Extract payload as CodeChunkPayload
			payloadMap := qdrant.PayloadToMap(point.Payload)
			data, _ := json.Marshal(payloadMap)
			var chunk models.CodeChunkPayload
			if err := json.Unmarshal(data, &chunk); err != nil {
				continue
			}

			key := chunk.CodeHash
			if existing, ok := allResults[key]; ok {
				existing.FusionScore += float64(point.Score)
				existing.Rank++
			} else {
				allResults[key] = &SearchResult{
					CodeChunkPayload: chunk,
					FusionScore:      float64(point.Score),
					Rank:             1,
				}
			}
		}
	}

	// Sort by fusion score and apply filter
	sortedResults := make([]*SearchResult, 0, len(allResults))
	for _, result := range allResults {
		if matchesFilter(result.CodeChunkPayload, plan.Filter) {
			sortedResults = append(sortedResults, result)
		}
	}

	// Sort by fusion score descending
	sort.Slice(sortedResults, func(i, j int) bool {
		return sortedResults[i].FusionScore > sortedResults[j].FusionScore
	})

	// Limit to topK
	if len(sortedResults) > topK {
		sortedResults = sortedResults[:topK]
	}

	// Extract final results
	finalResults := make([]CodeChunkPayload, len(sortedResults))
	for i, result := range sortedResults {
		finalResults[i] = result.CodeChunkPayload
	}

	return map[string]interface{}{
		"intent":  plan.Intent,
		"results": finalResults,
	}, nil
}

// matchesFilter checks if a code chunk matches the given filter criteria
func matchesFilter(chunk models.CodeChunkPayload, filter models.QueryFilter) bool {
	if len(filter.Languages) > 0 {
		found := false
		for _, lang := range filter.Languages {
			if chunk.Language == lang {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if len(filter.PathPrefix) > 0 {
		found := false
		for _, prefix := range filter.PathPrefix {
			if strings.HasPrefix(chunk.FilePath, prefix) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if len(filter.NodeTypes) > 0 {
		found := false
		for _, nodeType := range filter.NodeTypes {
			if chunk.NodeType == nodeType {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	lines := chunk.EndLine - chunk.StartLine + 1
	if filter.MinLines > 0 && lines < filter.MinLines {
		return false
	}
	if filter.MaxLines > 0 && lines > filter.MaxLines {
		return false
	}

	return true
}

func (s *Server) writeResponse(writer *bufio.Writer, id interface{}, result interface{}) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(resp)
	writeMessage(writer, data)
}

func (s *Server) writeError(writer *bufio.Writer, id interface{}, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
		},
	}
	data, _ := json.Marshal(resp)
	writeMessage(writer, data)
}

func formatResult(result interface{}) string {
	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data)
}

func readMessage(reader *bufio.Reader) ([]byte, error) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}

		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			continue
		}

		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "content-length:") {
			value := strings.TrimSpace(trimmed[len("Content-Length:"):])
			length, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %s", value)
			}

			// Expect blank line before payload.
			if _, err := reader.ReadString('\n'); err != nil {
				return nil, err
			}

			buf := make([]byte, length)
			if _, err := io.ReadFull(reader, buf); err != nil {
				return nil, err
			}
			return buf, nil
		}

		// Newline-delimited JSON (spec-compliant)
		return []byte(trimmed), nil
	}
}

func writeMessage(writer *bufio.Writer, data []byte) {
	writer.Write(data)
	writer.WriteByte('\n')
	writer.Flush()
}
