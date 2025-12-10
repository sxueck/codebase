package mcp

import (
	"bufio"
	"codebase/internal/analyzer"
	"codebase/internal/embeddings"
	"codebase/internal/indexer"
	"codebase/internal/llm"
	"codebase/internal/models"
	"codebase/internal/qdrant"
	"encoding/json"
	"fmt"
	"io"
	"os"
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

type Server struct {
	qdrantClient *qdrant.Client
	embedClient  *embeddings.Client
	llmClient    *llm.Client
	analyzer     *analyzer.Analyzer
}

func NewServer() (*Server, error) {
	qc, err := qdrant.NewClient()
	if err != nil {
		return nil, err
	}

	ec := embeddings.NewClient()
	lc := llm.NewClient()
	az := analyzer.NewAnalyzer(qc, lc)

	return &Server{
		qdrantClient: qc,
		embedClient:  ec,
		llmClient:    lc,
		analyzer:     az,
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
			"name":        "search_code",
			"description": "Search codebase using natural language query",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]string{"type": "string"},
					"top_k": map[string]string{"type": "integer"},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "find_redundant_code",
			"description": "Find duplicate or redundant code in the codebase",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"threshold": map[string]string{"type": "number"},
					"languages": map[string]interface{}{
						"type":  "array",
						"items": map[string]string{"type": "string"},
					},
				},
			},
		},
		{
			"name":        "code_query",
			"description": "Universal natural language code query interface",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]string{"type": "string"},
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
	case "search_code":
		result, err = s.handleSearchCode(params.Arguments)
	case "find_redundant_code":
		result, err = s.handleFindRedundant(params.Arguments)
	case "code_query":
		result, err = s.handleCodeQuery(params.Arguments)
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

	vec, err := s.embedClient.Embed(input.Query)
	if err != nil {
		return nil, err
	}

	results, err := s.qdrantClient.Search(indexer.CollectionName, vec, uint64(input.TopK))
	if err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Server) handleFindRedundant(args json.RawMessage) (interface{}, error) {
	var input struct {
		Threshold float64  `json:"threshold"`
		Languages []string `json:"languages"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}

	if input.Threshold == 0 {
		input.Threshold = 0.92
	}

	plan := models.QueryPlan{
		Intent:     models.IntentDuplicate,
		SubQueries: []string{},
		Filter: models.QueryFilter{
			Languages: input.Languages,
			NodeTypes: []string{"function", "method"},
			MinLines:  5,
			MaxLines:  300,
		},
		Threshold: input.Threshold,
	}

	groups, err := s.analyzer.FindDuplicates(plan)
	if err != nil {
		return nil, err
	}

	return groups, nil
}

func (s *Server) handleCodeQuery(args json.RawMessage) (interface{}, error) {
	var input struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}

	plan, err := s.llmClient.BuildQueryPlan(input.Query)
	if err != nil {
		return nil, err
	}

	switch plan.Intent {
	case models.IntentSearch:
		vec, err := s.embedClient.Embed(input.Query)
		if err != nil {
			return nil, err
		}
		return s.qdrantClient.Search(indexer.CollectionName, vec, 10)

	case models.IntentDuplicate:
		return s.analyzer.FindDuplicates(*plan)

	default:
		return map[string]string{"status": "intent not implemented"}, nil
	}
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
