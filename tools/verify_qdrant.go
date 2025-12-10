package main

import (
	"codebase/internal/config"
	"codebase/internal/qdrant"
	"fmt"
	"os"

	qdrantpb "github.com/qdrant/go-client/qdrant"
)

func main() {
	if err := config.LoadFromUserConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
	}

	qc, err := qdrant.NewClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create qdrant client: %v\n", err)
		os.Exit(1)
	}
	defer qc.Close()

	const collectionName = "codebase_knowledge"
	var totalPoints int
	var offset *qdrantpb.PointId = nil
	limit := uint32(100)

	fmt.Printf("Checking collection: %s\n", collectionName)

	for {
		points, nextOffset, err := qc.Scroll(collectionName, limit, offset)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error scrolling: %v\n", err)
			break
		}

		totalPoints += len(points)
		if len(points) > 0 {
			fmt.Printf("  Batch: %d points\n", len(points))
		}

		if nextOffset == nil || len(points) == 0 {
			break
		}
		offset = nextOffset
	}

	fmt.Printf("\n✓ Total points in collection: %d\n", totalPoints)
}
