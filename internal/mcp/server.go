package mcp

import (
	"bufio"
	"codebase/internal/config"
	"codebase/internal/embeddings"
	"codebase/internal/indexer"
	"codebase/internal/models"
	"codebase/internal/parser"
	"codebase/internal/qdrant"
	"codebase/internal/utils"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
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

	rootDir        string
	indexer        *indexer.Indexer
	ignorePatterns []string

	watcher   *fsnotify.Watcher
	watchDone chan struct{}
	watchWg   sync.WaitGroup
}

// Close releases any resources held by the server. Safe to call multiple
// times. Used by short-lived CLI commands that create a server for a
// single request.
func (s *Server) Close() {
	if s == nil {
		return
	}
	if s.watcher != nil {
		if s.watchDone != nil {
			close(s.watchDone)
		}
		s.watchWg.Wait()
		_ = s.watcher.Close()
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

	normalizedRoot, err := utils.NormalizeProjectRoot(rootDir)
	if err != nil {
		return nil, err
	}

	projectID, err := utils.ComputeProjectID(normalizedRoot)
	if err != nil {
		return nil, err
	}
	collection := indexer.CollectionName(projectID)

	ec := embeddings.NewClient()

	s := &Server{
		qdrantClient:   qc,
		embedClient:    ec,
		collection:     collection,
		rootDir:        normalizedRoot,
		ignorePatterns: utils.LoadGitIgnorePatterns(normalizedRoot),
	}

	idx := indexer.NewIndexer(qc, ec)
	idx.RegisterParser(string(parser.LanguageGo), parser.NewGoParser())
	idx.RegisterParser(string(parser.LanguagePython), parser.NewPythonParser())
	idx.RegisterParser(string(parser.LanguageJavaScript), parser.NewJavaScriptParser())
	idx.RegisterParser(string(parser.LanguageTypeScript), parser.NewTypeScriptParser())
	s.indexer = idx

	if err := s.startWatcher(); err != nil {
		fmt.Fprintf(os.Stderr, "[MCP WARN] Failed to start file watcher: %v\n", err)
	}

	return s, nil
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
			"name":        "codebase-retrieval",
			"description": "Semantic code search tool. Use this tool to find relevant code snippets, functions, or context within the repository when you need to answer user queries with specific code references. It helps anchor your responses to the actual codebase.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Natural language description of what you need to inspect in this repo. Include specific symbols, file names, or features to maximize recall.",
					},
					"top_k": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of code snippets to return (default 5).",
					},
					"project_path": map[string]interface{}{
						"type":        "string",
						"description": "Optional absolute path to the project root directory to search. If not provided, uses the default directory specified when starting the MCP server.",
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
		input.TopK = 5
	}

	// Determine which collection and root to use based on project_path
	collection := s.collectionName()
	searchRoot := s.rootDir

	if input.ProjectPath != "" {
		normalized, err := utils.NormalizeProjectRoot(input.ProjectPath)
		if err != nil {
			return nil, fmt.Errorf("invalid project_path: %w", err)
		}
		searchRoot = normalized

		projectID, err := utils.ComputeProjectID(searchRoot)
		if err != nil {
			return nil, fmt.Errorf("failed to compute project ID: %w", err)
		}
		collection = indexer.CollectionName(projectID)
	}

	// Perform simple semantic search without query planning
	return s.simpleSearchWithCollection(input.Query, input.TopK, collection, searchRoot)
}

// simpleSearchWithCollection performs basic semantic search on a specific collection
// It uses a diversity-aware strategy: fetching more candidates and prioritizing unique files
// to ensure a broader coverage of the codebase.
func (s *Server) simpleSearchWithCollection(query string, topK int, collection string, rootPath string) (interface{}, error) {
	vec, err := s.embedClient.Embed(query)
	if err != nil {
		return nil, err
	}

	// Strategy: Fetch more candidates (3x topK) to allow for filtering and diversity.
	// This helps avoid crowding the results with many chunks from a single relevant file.
	searchLimit := topK * 3
	// Ensure a reasonable minimum to have enough candidates for reranking
	if searchLimit < 20 {
		searchLimit = 20
	}

	results, err := s.qdrantClient.Search(collection, vec, uint64(searchLimit))
	if err != nil {
		return nil, err
	}

	type candidate struct {
		payload  map[string]interface{}
		score    float32
		fileKey  string
		relPath  string
	}

	var candidates []candidate

	// Pre-process results to check file existence and path normalization
	for _, hit := range results {
		payload := qdrant.PayloadToMap(hit.Payload)
		filePath, ok := payload["file_path"].(string)
		if !ok {
			continue
		}

		// Handle both absolute and relative paths in the index
		checkPath := filePath
		if !filepath.IsAbs(checkPath) {
			checkPath = filepath.Join(rootPath, checkPath)
		}

		// Verify file exists on disk to avoid returning deleted files.
		// If Stat fails for any reason, skip the hit to avoid leaking unusable paths.
		if _, err := os.Stat(checkPath); err != nil {
			continue
		}

		// Use a normalized absolute path as a stable grouping key so the diversity
		// filter doesn't break if the index mixes relative and absolute paths.
		fileKey := checkPath
		if abs, err := filepath.Abs(checkPath); err == nil {
			fileKey = abs
		}
		fileKey = filepath.Clean(fileKey)

		// Calculate relative path for display
		relPath := filePath
		if rel, err := filepath.Rel(rootPath, checkPath); err == nil {
			relPath = rel
		}

		candidates = append(candidates, candidate{
			payload:  payload,
			score:    hit.Score,
			fileKey:  fileKey,
			relPath:  relPath,
		})
	}

	var finalResults []map[string]interface{}
	fileCounts := make(map[string]int)
	maxChunksPerFilePass1 := 1 // Pass 1: enforce unique-file-first
	usedIndices := make(map[int]bool)

	// Pass 1: Diversity focused - take at most 1 chunk per file to maximize coverage.
	for i, item := range candidates {
		if len(finalResults) >= topK {
			break
		}
		if fileCounts[item.fileKey] < maxChunksPerFilePass1 {
			finalResults = append(finalResults, map[string]interface{}{
				"file_path":  item.relPath,
				"start_line": item.payload["start_line"],
				"end_line":   item.payload["end_line"],
				"content":    item.payload["content"],
				"score":      item.score,
			})
			fileCounts[item.fileKey]++
			usedIndices[i] = true
		}
	}

	// Pass 2: Fill remaining slots if needed (relax diversity constraint)
	if len(finalResults) < topK {
		for i, item := range candidates {
			if len(finalResults) >= topK {
				break
			}
			if !usedIndices[i] {
				finalResults = append(finalResults, map[string]interface{}{
					"file_path":  item.relPath,
					"start_line": item.payload["start_line"],
					"end_line":   item.payload["end_line"],
					"content":    item.payload["content"],
					"score":      item.score,
				})
				usedIndices[i] = true
			}
		}
	}

	return finalResults, nil
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

func (s *Server) shouldSkipWatchDir(path string) bool {
	if s == nil || strings.TrimSpace(s.rootDir) == "" {
		return false
	}

	relPath, err := filepath.Rel(s.rootDir, path)
	if err != nil {
		return true
	}
	relPath = filepath.ToSlash(relPath)
	if strings.HasPrefix(relPath, "..") {
		return true
	}
	return utils.ShouldSkipDir(relPath, filepath.Base(path), s.ignorePatterns)
}

func (s *Server) addWatcherForDir(path string) {
	if s == nil || s.watcher == nil {
		return
	}
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if s.shouldSkipWatchDir(p) {
			return filepath.SkipDir
		}
		_ = s.watcher.Add(p)
		return nil
	})
}

// startWatcher sets up a recursive file watcher rooted at the server's
// project directory and spawns a goroutine that debounces change events
// and triggers incremental re-indexing.
func (s *Server) startWatcher() error {
	if strings.TrimSpace(s.rootDir) == "" || s.indexer == nil {
		return nil
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	// Watch all existing directories under rootDir except heavy or ignored paths.
	if err := filepath.WalkDir(s.rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if s.shouldSkipWatchDir(path) {
			return filepath.SkipDir
		}
		return w.Add(path)
	}); err != nil {
		_ = w.Close()
		return err
	}

	s.watcher = w
	s.watchDone = make(chan struct{})

	s.watchWg.Add(1)
	go s.watchLoop()

	return nil
}

func (s *Server) watchLoop() {
	defer s.watchWg.Done()

	if s.watcher == nil {
		return
	}

	// Debounce rapid change bursts into a single incremental index run.
	debounce := 5 * time.Second
	reindexRequested := false
	timer := time.NewTimer(debounce)
	if !timer.Stop() {
		<-timer.C
	}

	for {
		select {
		case <-s.watchDone:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case ev, ok := <-s.watcher.Events:
			if !ok {
				return
			}

			// If a new directory is created, start watching it as well.
			if ev.Op&fsnotify.Create == fsnotify.Create {
				fi, err := os.Stat(ev.Name)
				if err == nil && fi.IsDir() {
					s.addWatcherForDir(ev.Name)
				}
			}

			// Any create/write/remove/rename event is a signal that the
			// on-disk state may have changed; schedule an incremental index.
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0 {
				reindexRequested = true
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				_ = timer.Reset(debounce)
			}
		case <-timer.C:
			if reindexRequested {
				reindexRequested = false
				s.runIncrementalIndex()
			}
			_ = timer.Reset(debounce)
		}
	}
}


func (s *Server) runIncrementalIndex() {
	if s.indexer == nil || strings.TrimSpace(s.rootDir) == "" {
		return
	}

	fmt.Fprintf(os.Stderr, "[MCP] Detected file changes, running incremental index...\n")
	if err := s.indexer.IndexProject(s.rootDir); err != nil {
		fmt.Fprintf(os.Stderr, "[MCP WARN] Incremental index failed: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[MCP] Incremental index completed.\n")
	}
}
