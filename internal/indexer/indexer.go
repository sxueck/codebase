package indexer

import (
	"codebase/internal/embeddings"
	"codebase/internal/models"
	"codebase/internal/parser"
	"codebase/internal/qdrant"
	"codebase/internal/utils"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	qdrantpb "github.com/qdrant/go-client/qdrant"
)

const (
	defaultCollectionName = "codebase_default"
	collectionPrefix      = "codebase_"
	NumWorkers            = 4
	BatchSize             = 10
)

// CollectionName returns the Qdrant collection name for a given project ID.
// If projectID is empty, the shared default collection is used.
func CollectionName(projectID string) string {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return defaultCollectionName
	}
	return fmt.Sprintf("%s%s", collectionPrefix, projectID)
}

type Indexer struct {
	qdrant     *qdrant.Client
	embeddings *embeddings.Client
	parsers    map[string]parser.LanguageParser
	projectID  string
	collection string
}

func NewIndexer(qc *qdrant.Client, ec *embeddings.Client) *Indexer {
	return &Indexer{
		qdrant:     qc,
		embeddings: ec,
		parsers:    make(map[string]parser.LanguageParser),
	}
}

func (idx *Indexer) RegisterParser(lang string, p parser.LanguageParser) {
	idx.parsers[lang] = p
}

func (idx *Indexer) IndexProject(rootPath string) error {
	normalizedRoot, err := utils.NormalizeProjectRoot(rootPath)
	if err != nil {
		return fmt.Errorf("failed to normalize project root: %w", err)
	}

	projectID, err := utils.ComputeProjectID(normalizedRoot)
	if err != nil {
		return fmt.Errorf("failed to compute project id: %w", err)
	}
	idx.projectID = projectID
	idx.collection = CollectionName(projectID)
	shortID := projectID
	if len(shortID) > 12 {
		shortID = projectID[:12]
	}
	fmt.Printf("→ Project fingerprint: %s\n", shortID)
	fmt.Printf("→ Using collection: %s\n", idx.collection)

	files, err := utils.GetAllSourceFiles(normalizedRoot)
	if err != nil {
		return err
	}
	fmt.Printf("✓ Found %d source files\n", len(files))

	if len(files) == 0 {
		fmt.Println("⚠ No source files found to index")
		return nil
	}

	// Load previous file hashes for incremental indexing.
	prevHashes, err := loadFileHashes(projectID)
	if err != nil {
		return fmt.Errorf("failed to load file hashes: %w", err)
	}
	prevHashes = canonicalizeHashKeys(prevHashes, normalizedRoot)

	currentHashes := make(map[string]string, len(files))
	var changedFiles []string

	for _, f := range files {
		hash, herr := hashFile(f)
		if herr != nil {
			fmt.Fprintf(os.Stderr, "✗ Failed to hash %s: %v\n", f, herr)
			continue
		}
		key := normalizeFilePath(f)
		currentHashes[key] = hash
		if prev, ok := prevHashes[key]; !ok || prev != hash {
			changedFiles = append(changedFiles, f)
		}
	}

	var deletedFiles []string
	for path := range prevHashes {
		if _, ok := currentHashes[path]; !ok {
			deletedFiles = append(deletedFiles, path)
		}
	}

	fmt.Printf("→ Incremental index: %d added/modified, %d deleted, %d total files\n", len(changedFiles), len(deletedFiles), len(files))

	if len(changedFiles) == 0 && len(deletedFiles) == 0 {
		fmt.Println("✓ No changes detected, index is already up to date")
		return nil
	}

	// Delete vectors for files that have been removed from the filesystem.
	for _, normalizedPath := range deletedFiles {
		displayPath := filepath.FromSlash(normalizedPath)
		if err := idx.deleteFilePoints(normalizedPath); err != nil {
			fmt.Fprintf(os.Stderr, "✗ Error deleting vectors for removed file %s: %v\n", displayPath, err)
		} else {
			fmt.Printf("✓ Deleted vectors for removed file %s\n", displayPath)
		}
	}

	// Index only added or modified files.
	if len(changedFiles) > 0 {
		var wg sync.WaitGroup
		fileCh := make(chan string, len(changedFiles))

		for i := 0; i < NumWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				idx.processWorker(fileCh)
			}()
		}

		for _, f := range changedFiles {
			fileCh <- f
		}
		close(fileCh)
		wg.Wait()
	}

	if err := saveFileHashes(idx.projectID, currentHashes); err != nil {
		return fmt.Errorf("failed to save file hashes: %w", err)
	}

	fmt.Println("✓ Indexing completed")
	return nil
}

func (idx *Indexer) processWorker(fileCh <-chan string) {
	for path := range fileCh {
		if err := idx.processFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %v\n", path, err)
		}
	}
}

func (idx *Indexer) processFile(path string) error {
	if idx.collection == "" {
		return fmt.Errorf("collection name is not set on indexer")
	}
	// Normalize path for consistent storage in Qdrant and stable deletion.
	normalizedPath := normalizeFilePath(path)

	// For modified files, clear any existing vectors for this file before
	// re-indexing so that removed functions do not leave stale points.
	if err := idx.deleteFilePoints(normalizedPath); err != nil {
		fmt.Fprintf(os.Stderr, "✗ Error deleting existing vectors for %s: %v\n", path, err)
	}

	lang := utils.DetectLanguage(path)
	if lang == "" {
		return nil
	}

	p, ok := idx.parsers[lang]
	if !ok {
		return nil
	}

	code, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	funcs, err := p.ExtractFunctions(path, code)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Error parsing %s: %v\n", path, err)
		return err
	}

	if len(funcs) == 0 {
		return nil
	}

	fmt.Printf("→ Processing %s (%d functions)\n", path, len(funcs))

	// Build embedding texts that combine code with richer AST metadata for
	// hybrid retrieval (symbol, import, and signature level signals).
	contents := make([]string, 0, len(funcs))
	for _, fn := range funcs {
		metaLines := []string{
			fmt.Sprintf("file_path: %s", normalizedPath),
			fmt.Sprintf("language: %s", lang),
			fmt.Sprintf("node_name: %s", fn.Name),
			fmt.Sprintf("node_type: %s", fn.NodeType),
		}
		if fn.PackageName != "" {
			metaLines = append(metaLines, fmt.Sprintf("package: %s", fn.PackageName))
		}
		if len(fn.Imports) > 0 {
			metaLines = append(metaLines, fmt.Sprintf("imports: %s", strings.Join(fn.Imports, ", ")))
		}
		if fn.Signature != "" {
			metaLines = append(metaLines, fmt.Sprintf("signature: %s", fn.Signature))
		}
		if fn.Receiver != "" {
			metaLines = append(metaLines, fmt.Sprintf("receiver: %s", fn.Receiver))
		}
		if fn.Doc != "" {
			metaLines = append(metaLines, fmt.Sprintf("doc: %s", fn.Doc))
		}
		if len(fn.Callees) > 0 {
			metaLines = append(metaLines, fmt.Sprintf("callees: %s", strings.Join(fn.Callees, ", ")))
		}
		// Add parameter types
		if len(fn.ParamTypes) > 0 {
			metaLines = append(metaLines, fmt.Sprintf("param_types: %s", strings.Join(fn.ParamTypes, ", ")))
		}
		// Add return types
		if len(fn.ReturnTypes) > 0 {
			metaLines = append(metaLines, fmt.Sprintf("return_types: %s", strings.Join(fn.ReturnTypes, ", ")))
		}
		// Add error handling flag
		if fn.HasErrorReturn {
			metaLines = append(metaLines, "has_error_return: true")
		}

		text := fmt.Sprintf("%s\n\n%s", strings.Join(metaLines, "\n"), fn.Content)
		contents = append(contents, text)
	}

	vectors, err := idx.embeddings.EmbedBatch(contents)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Error embedding %s: %v\n", path, err)
		return err
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return fmt.Errorf("no embedding vectors returned for %s", path)
	}

	// Ensure Qdrant collection lazily using the actual embedding dimension so we
	// don't need a separate probe request.
	vectorSize := uint64(len(vectors[0]))
	if err := idx.qdrant.EnsureCollection(idx.collection, vectorSize); err != nil {
		return err
	}

	points := make([]*qdrantpb.PointStruct, 0, len(funcs))
	for i, fn := range funcs {
		hash := utils.HashContent(fn.Content)
		id := contentHashToPointID(hash)
		payload := models.CodeChunkPayload{
			FilePath:       normalizedPath,
			Language:       lang,
			NodeType:       fn.NodeType,
			NodeName:       fn.Name,
			StartLine:      fn.StartLine,
			EndLine:        fn.EndLine,
			CodeHash:       hash,
			Content:        fn.Content,
			PackageName:    fn.PackageName,
			Imports:        fn.Imports,
			Signature:      fn.Signature,
			Receiver:       fn.Receiver,
			Doc:            fn.Doc,
			Callees:        fn.Callees,
			ParamTypes:     fn.ParamTypes,
			ReturnTypes:    fn.ReturnTypes,
			HasErrorReturn: fn.HasErrorReturn,
		}

		payloadMap := map[string]interface{}{
			"file_path":        payload.FilePath,
			"language":         payload.Language,
			"node_type":        payload.NodeType,
			"node_name":        payload.NodeName,
			"start_line":       payload.StartLine,
			"end_line":         payload.EndLine,
			"code_hash":        payload.CodeHash,
			"content":          payload.Content,
			"package_name":     payload.PackageName,
			"imports":          payload.Imports,
			"signature":        payload.Signature,
			"receiver":         payload.Receiver,
			"doc":              payload.Doc,
			"callees":          payload.Callees,
			"param_types":      payload.ParamTypes,
			"return_types":     payload.ReturnTypes,
			"has_error_return": payload.HasErrorReturn,
		}

		points = append(points, &qdrantpb.PointStruct{
			Id: &qdrantpb.PointId{
				PointIdOptions: &qdrantpb.PointId_Num{
					Num: id,
				},
			},
			Vectors: &qdrantpb.Vectors{
				VectorsOptions: &qdrantpb.Vectors_Vector{
					Vector: &qdrantpb.Vector{
						Data: vectors[i],
					},
				},
			},
			Payload: qdrant.MapToPayload(payloadMap),
		})
	}

	err = idx.qdrant.Upsert(idx.collection, points)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Error upserting %s: %v\n", path, err)
		return err
	}

	fmt.Printf("✓ Indexed %s (%d vectors)\n", path, len(points))
	return nil
}

// contentHashToPointID converts a hex-encoded SHA-256 hash string into a 64-bit
// numeric ID that is accepted by Qdrant's `PointId_Num` field. We take the
// first 8 bytes of the hash and interpret them as a big-endian uint64.
func contentHashToPointID(hash string) uint64 {
	// utils.HashContent already uses SHA-256, but we defensively recompute in
	// case the implementation changes while keeping behaviour stable here.
	h := sha256.Sum256([]byte(hash))
	return binary.BigEndian.Uint64(h[:8])
}

// hashFile computes a stable hash for a file's entire contents. It is used to
// detect added/modified files for incremental indexing.
func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return utils.HashContent(string(data)), nil
}

func normalizeFilePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	abs := path
	if !filepath.IsAbs(abs) {
		if a, err := filepath.Abs(abs); err == nil {
			abs = a
		}
	}
	abs = filepath.Clean(abs)
	normalized := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" {
		normalized = strings.ToLower(normalized)
	}
	return normalized
}

func canonicalizeHashKeys(hashes map[string]string, normalizedRoot string) map[string]string {
	if len(hashes) == 0 {
		return hashes
	}
	root := strings.TrimSpace(normalizedRoot)
	if root == "" {
		return hashes
	}
	root = filepath.Clean(root)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}

	out := make(map[string]string, len(hashes))
	for k, v := range hashes {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		p := filepath.FromSlash(key)
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		out[normalizeFilePath(p)] = v
	}
	return out
}

// loadFileHashes loads the last-seen file hash map from disk. It is stored as
// a JSON file under ~/.codebase scoped by the project ID.
func loadFileHashes(projectID string) (map[string]string, error) {
	statePath, err := fileHashStatePath(projectID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, err
	}

	var hashes map[string]string
	if err := json.Unmarshal(data, &hashes); err != nil {
		return nil, err
	}
	if hashes == nil {
		hashes = make(map[string]string)
	}
	return hashes, nil
}

// saveFileHashes persists the current file hash map so that the next indexing
// run can cheaply detect which files have changed.
func saveFileHashes(projectID string, hashes map[string]string) error {
	statePath, err := fileHashStatePath(projectID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(hashes, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath, data, 0o644)
}

func fileHashStatePath(projectID string) (string, error) {
	stateDir, err := utils.UserStateDir()
	if err != nil {
		return "", err
	}
	if projectID == "" {
		projectID = "default"
	}
	fileName := fmt.Sprintf("%s_file_hashes.json", projectID)
	return filepath.Join(stateDir, fileName), nil
}

// ClearProjectState removes any local on-disk state associated with a project.
// Currently this is the file-hash map used for incremental indexing.
func ClearProjectState(projectID string) error {
	statePath, err := fileHashStatePath(projectID)
	if err != nil {
		return err
	}
	if err := os.Remove(statePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}

// deleteFilePoints removes all vectors in Qdrant whose payload file_path
// matches the given path.
func (idx *Indexer) deleteFilePoints(path string) error {
	if idx.collection == "" {
		return fmt.Errorf("collection name is not set on indexer")
	}
	filter := &qdrantpb.Filter{
		Must: []*qdrantpb.Condition{
			{
				ConditionOneOf: &qdrantpb.Condition_Field{
					Field: &qdrantpb.FieldCondition{
						Key: "file_path",
						Match: &qdrantpb.Match{
							MatchValue: &qdrantpb.Match_Keyword{
								Keyword: path,
							},
						},
					},
				},
			},
		},
	}

	return idx.qdrant.DeleteByFilter(idx.collection, filter)
}
