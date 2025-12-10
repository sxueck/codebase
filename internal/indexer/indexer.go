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
	"sync"

	qdrantpb "github.com/qdrant/go-client/qdrant"
)

const (
	CollectionName = "codebase_knowledge"
	NumWorkers     = 4
	BatchSize      = 10
)

type Indexer struct {
	qdrant     *qdrant.Client
	embeddings *embeddings.Client
	parsers    map[string]parser.LanguageParser
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
	files, err := utils.GetAllSourceFiles(rootPath)
	if err != nil {
		return err
	}
	fmt.Printf("✓ Found %d source files\n", len(files))

	if len(files) == 0 {
		fmt.Println("⚠ No source files found to index")
		return nil
	}

	// Load previous file hashes for incremental indexing.
	prevHashes, err := loadFileHashes(rootPath)
	if err != nil {
		return fmt.Errorf("failed to load file hashes: %w", err)
	}

	currentHashes := make(map[string]string, len(files))
	var changedFiles []string

	for _, f := range files {
		hash, herr := hashFile(f)
		if herr != nil {
			fmt.Fprintf(os.Stderr, "✗ Failed to hash %s: %v\n", f, herr)
			continue
		}
		// Normalize path to use forward slashes for consistent comparison
		normalizedPath := filepath.ToSlash(f)
		currentHashes[normalizedPath] = hash
		if prev, ok := prevHashes[normalizedPath]; !ok || prev != hash {
			changedFiles = append(changedFiles, f)
		}
	}

	var deletedFiles []string
	for path := range prevHashes {
		// Convert stored path back to OS-specific format for deletion
		osPath := filepath.FromSlash(path)
		if _, ok := currentHashes[path]; !ok {
			deletedFiles = append(deletedFiles, osPath)
		}
	}

	fmt.Printf("→ Incremental index: %d added/modified, %d deleted, %d total files\n", len(changedFiles), len(deletedFiles), len(files))

	if len(changedFiles) == 0 && len(deletedFiles) == 0 {
		fmt.Println("✓ No changes detected, index is already up to date")
		return nil
	}

	// Detect vector size by creating a sample embedding
	fmt.Println("→ Detecting vector dimensions...")
	sampleVec, err := idx.embeddings.Embed("sample text for dimension detection")
	if err != nil {
		return fmt.Errorf("failed to detect vector dimensions: %w", err)
	}
	vectorSize := uint64(len(sampleVec))
	fmt.Printf("✓ Detected vector dimension: %d\n", vectorSize)

	// Ensure collection with detected vector size
	if err := idx.qdrant.EnsureCollection(CollectionName, vectorSize); err != nil {
		return err
	}
	fmt.Printf("✓ Collection '%s' ensured with dimension %d\n", CollectionName, vectorSize)

	// Delete vectors for files that have been removed from the filesystem.
	for _, path := range deletedFiles {
		if err := idx.deleteFilePoints(path); err != nil {
			fmt.Fprintf(os.Stderr, "✗ Error deleting vectors for removed file %s: %v\n", path, err)
		} else {
			fmt.Printf("✓ Deleted vectors for removed file %s\n", path)
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

	if err := saveFileHashes(rootPath, currentHashes); err != nil {
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
	// Normalize path for consistent storage in Qdrant
	normalizedPath := filepath.ToSlash(path)
	
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

    // Build embedding texts that include both code and lightweight metadata so
    // that natural-language queries mentioning file paths or symbol names
    // (e.g. "utils helper functions") can be matched more reliably.
    contents := make([]string, 0, len(funcs))
    for _, fn := range funcs {
        text := fmt.Sprintf(
            "file_path: %s\nlanguage: %s\nnode_name: %s\nnode_type: %s\n\n%s",
            normalizedPath, lang, fn.Name, fn.NodeType, fn.Content,
        )
        contents = append(contents, text)
    }

	vectors, err := idx.embeddings.EmbedBatch(contents)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Error embedding %s: %v\n", path, err)
		return err
	}

	points := make([]*qdrantpb.PointStruct, 0, len(funcs))
	for i, fn := range funcs {
		hash := utils.HashContent(fn.Content)
		id := contentHashToPointID(hash)
		payload := models.CodeChunkPayload{
			FilePath:  normalizedPath,
			Language:  lang,
			NodeType:  fn.NodeType,
			NodeName:  fn.Name,
			StartLine: fn.StartLine,
			EndLine:   fn.EndLine,
			CodeHash:  hash,
			Content:   fn.Content,
		}

		payloadMap := map[string]interface{}{
			"file_path":  payload.FilePath,
			"language":   payload.Language,
			"node_type":  payload.NodeType,
			"node_name":  payload.NodeName,
			"start_line": payload.StartLine,
			"end_line":   payload.EndLine,
			"code_hash":  payload.CodeHash,
			"content":    payload.Content,
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

	err = idx.qdrant.Upsert(CollectionName, points)
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

// loadFileHashes loads the last-seen file hash map from disk. It is stored as
// a simple JSON map at <rootPath>/file_hashes.json.
func loadFileHashes(rootPath string) (map[string]string, error) {
	statePath := filepath.Join(rootPath, "file_hashes.json")
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
func saveFileHashes(rootPath string, hashes map[string]string) error {
	statePath := filepath.Join(rootPath, "file_hashes.json")
	data, err := json.MarshalIndent(hashes, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath, data, 0o644)
}

// deleteFilePoints removes all vectors in Qdrant whose payload file_path
// matches the given path.
func (idx *Indexer) deleteFilePoints(path string) error {
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

	return idx.qdrant.DeleteByFilter(CollectionName, filter)
}
