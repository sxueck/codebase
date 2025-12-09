package indexer

import (
	"codebase/internal/embeddings"
	"codebase/internal/models"
	"codebase/internal/parser"
	"codebase/internal/qdrant"
	"codebase/internal/utils"
	"fmt"
	"os"
	"sync"

	qdrantpb "github.com/qdrant/go-client/qdrant"
)

const (
	CollectionName = "codebase_knowledge"
	VectorSize     = 1536
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
	if err := idx.qdrant.EnsureCollection(CollectionName, VectorSize); err != nil {
		return err
	}

	files, err := utils.GetAllSourceFiles(rootPath)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	fileCh := make(chan string, len(files))

	for i := 0; i < NumWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			idx.processWorker(fileCh)
		}()
	}

	for _, f := range files {
		fileCh <- f
	}
	close(fileCh)
	wg.Wait()

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
		return err
	}

	if len(funcs) == 0 {
		return nil
	}

	contents := make([]string, 0, len(funcs))
	for _, fn := range funcs {
		contents = append(contents, fn.Content)
	}

	vectors, err := idx.embeddings.EmbedBatch(contents)
	if err != nil {
		return err
	}

	points := make([]*qdrantpb.PointStruct, 0, len(funcs))
	for i, fn := range funcs {
		hash := utils.HashContent(fn.Content)
		payload := models.CodeChunkPayload{
			FilePath:  path,
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
				PointIdOptions: &qdrantpb.PointId_Uuid{
					Uuid: hash,
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

	return idx.qdrant.Upsert(CollectionName, points)
}
