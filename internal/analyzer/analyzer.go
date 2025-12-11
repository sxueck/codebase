package analyzer

import (
	"codebase/internal/indexer"
	"codebase/internal/models"
	"codebase/internal/qdrant"
	"codebase/internal/utils"
	"encoding/json"

	qdrantpb "github.com/qdrant/go-client/qdrant"
)

type Analyzer struct {
	qdrant     *qdrant.Client
	collection string
}

func NewAnalyzer(qc *qdrant.Client, _ interface{}, collection string) *Analyzer {
	return &Analyzer{
		qdrant:     qc,
		collection: collection,
	}
}

func (a *Analyzer) FindDuplicates(plan models.QueryPlan) ([]models.DuplicateGroup, error) {
	chunks, vectors, err := a.fetchAllVectors(plan.Filter)
	if err != nil {
		return nil, err
	}

	var candidates []models.PairCandidate
	for i := 0; i < len(vectors); i++ {
		for j := i + 1; j < len(vectors); j++ {
			score := utils.CosineSim(vectors[i], vectors[j])
			if score >= plan.Threshold && !isTrivialPair(chunks[i], chunks[j]) {
				candidates = append(candidates, models.PairCandidate{
					A:     chunks[i],
					B:     chunks[j],
					Score: score,
				})
			}
		}
	}

	confirmed := a.filterDuplicatePairs(candidates)
	groups := buildDuplicateGroups(confirmed)

	return groups, nil
}

func (a *Analyzer) fetchAllVectors(filter models.QueryFilter) ([]models.CodeChunkPayload, [][]float32, error) {
	var chunks []models.CodeChunkPayload
	var vectors [][]float32

	var offset *qdrantpb.PointId
	limit := uint32(100)
	collection := a.collection
	if collection == "" {
		collection = indexer.CollectionName("")
	}

	for {
		points, nextOffset, err := a.qdrant.Scroll(collection, limit, offset)
		if err != nil {
			return nil, nil, err
		}

		for _, point := range points {
			payloadMap := qdrant.PayloadToMap(point.Payload)

			var chunk models.CodeChunkPayload
			data, _ := json.Marshal(payloadMap)
			json.Unmarshal(data, &chunk)

			if matchesFilter(chunk, filter) {
				chunks = append(chunks, chunk)
				if vec := point.Vectors.GetVector(); vec != nil {
					vectors = append(vectors, vec.Data)
				}
			}
		}

		if nextOffset == nil {
			break
		}
		offset = nextOffset
	}

	return chunks, vectors, nil
}

func (a *Analyzer) filterDuplicatePairs(candidates []models.PairCandidate) []models.PairCandidate {
	// Directly return candidates that have passed the threshold check
	// No LLM-based secondary classification needed
	return candidates
}

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

	lines := chunk.EndLine - chunk.StartLine + 1
	if filter.MinLines > 0 && lines < filter.MinLines {
		return false
	}
	if filter.MaxLines > 0 && lines > filter.MaxLines {
		return false
	}

	return true
}

func isTrivialPair(a, b models.CodeChunkPayload) bool {
	if a.FilePath == b.FilePath {
		if a.StartLine == b.StartLine || a.EndLine == b.EndLine {
			return true
		}
	}

	aLines := a.EndLine - a.StartLine + 1
	bLines := b.EndLine - b.StartLine + 1
	if aLines < 3 || bLines < 3 {
		return true
	}

	return false
}

func buildDuplicateGroups(pairs []models.PairCandidate) []models.DuplicateGroup {
	if len(pairs) == 0 {
		return nil
	}

	parent := make(map[string]string)
	rank := make(map[string]int)

	var find func(string) string
	find = func(x string) string {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}

	union := func(x, y string) {
		px, py := find(x), find(y)
		if px == py {
			return
		}
		if rank[px] < rank[py] {
			parent[px] = py
		} else if rank[px] > rank[py] {
			parent[py] = px
		} else {
			parent[py] = px
			rank[px]++
		}
	}

	for _, pair := range pairs {
		keyA := pair.A.CodeHash
		keyB := pair.B.CodeHash
		if _, ok := parent[keyA]; !ok {
			parent[keyA] = keyA
			rank[keyA] = 0
		}
		if _, ok := parent[keyB]; !ok {
			parent[keyB] = keyB
			rank[keyB] = 0
		}
		union(keyA, keyB)
	}

	groups := make(map[string]*models.DuplicateGroup)
	groupScores := make(map[string][]float64)
	chunkMap := make(map[string]models.CodeChunkPayload)

	for _, pair := range pairs {
		chunkMap[pair.A.CodeHash] = pair.A
		chunkMap[pair.B.CodeHash] = pair.B

		root := find(pair.A.CodeHash)
		if _, ok := groups[root]; !ok {
			groups[root] = &models.DuplicateGroup{
				Chunks:   []models.CodeChunkPayload{},
				AvgScore: 0,
				Reason:   "Code similarity detected based on semantic analysis",
			}
		}
		groupScores[root] = append(groupScores[root], pair.Score)
	}

	for hash := range chunkMap {
		root := find(hash)
		if group, ok := groups[root]; ok {
			group.Chunks = append(group.Chunks, chunkMap[hash])
		}
	}

	// Compute average score per group
	for root, group := range groups {
		if scores, ok := groupScores[root]; ok && len(scores) > 0 {
			var sum float64
			for _, s := range scores {
				sum += s
			}
			group.AvgScore = sum / float64(len(scores))
		}
	}

	result := make([]models.DuplicateGroup, 0, len(groups))
	for _, group := range groups {
		result = append(result, *group)
	}

	return result
}
