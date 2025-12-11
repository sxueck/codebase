package mcp

import (
	"bufio"
	"codebase/internal/config"
	"codebase/internal/embeddings"
	"codebase/internal/indexer"
	"codebase/internal/models"
	"codebase/internal/qdrant"
	"codebase/internal/utils"
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

// CodeChunkPayload is kept for backwards compatibility with older code paths.
// New code should reference models.CodeChunkPayload directly.
type CodeChunkPayload = models.CodeChunkPayload

type Server struct {
	qdrantClient *qdrant.Client
	embedClient  *embeddings.Client
	collection   string
}

// Close releases any resources held by the server. Safe to call multiple
// times. Used by short-lived CLI commands that create a server for a
// single request.
func (s *Server) Close() {
	if s == nil {
		return
	}
	if s.qdrantClient != nil {
		s.qdrantClient.Close()
	}
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
	// layer based on env vars. Failures here should not prevent the server
	// from starting as long as required env vars are set.
	if err := config.LoadFromUserConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "[MCP WARN] Failed to load user config: %v\n", err)
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

	return &Server{
		qdrantClient: qc,
		embedClient:  ec,
		collection:   collection,
	}, nil
}

func (s *Server) Run() error {
	defer s.Close()

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
					"project_path": map[string]interface{}{
						"type":        "string",
						"description": "Optional absolute path to the project root directory to search. If not provided, uses the default directory specified when starting the MCP server. Use this to search across different projects or repositories.",
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

// HandleCodebaseRetrieval is the exported version for CLI access
func (s *Server) HandleCodebaseRetrieval(args json.RawMessage) (interface{}, error) {
	return s.handleCodebaseRetrieval(args)
}

func (s *Server) handleSearchCode(args json.RawMessage) (interface{}, error) {
	var input struct {
		Query       string `json:"query"`
		TopK        int    `json:"top_k"`
		ProjectPath string `json:"project_path"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}

	if input.TopK == 0 {
		input.TopK = 10
	}

	// Determine which collection to use based on project_path
	collection := s.collectionName()
	if input.ProjectPath != "" {
		projectID, err := utils.ComputeProjectID(input.ProjectPath)
		if err != nil {
			return nil, fmt.Errorf("invalid project_path: %w", err)
		}
		collection = indexer.CollectionName(projectID)
	}

	// Perform simple semantic search without query planning
	return s.simpleSearchWithCollection(input.Query, input.TopK, collection)
}

// simpleSearch performs basic semantic search without query planning
func (s *Server) simpleSearch(query string, topK int) (interface{}, error) {
	return s.simpleSearchWithCollection(query, topK, s.collectionName())
}

// simpleSearchWithCollection performs basic semantic search on a specific collection
func (s *Server) simpleSearchWithCollection(query string, topK int, collection string) (interface{}, error) {
	vec, err := s.embedClient.Embed(query)
	if err != nil {
		return nil, err
	}

	results, err := s.qdrantClient.Search(collection, vec, uint64(topK))
	if err != nil {
		return nil, err
	}
	return results, nil
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
