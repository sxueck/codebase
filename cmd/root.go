package cmd

import (
	"codebase/internal/embeddings"
	"codebase/internal/indexer"
	"codebase/internal/llm"
	"codebase/internal/mcp"
	"codebase/internal/qdrant"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "codebase",
	Short: "Codebase analysis tool with semantic search and duplicate detection",
	Long:  "A CLI tool for indexing, searching, and analyzing codebases using vector embeddings and LLM",
}

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Index codebase to vector database",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("dir")

		qc, err := qdrant.NewClient()
		if err != nil {
			return err
		}
		defer qc.Close()

		ec := embeddings.NewClient()
		idx := indexer.NewIndexer(qc, ec)

		fmt.Printf("Indexing project at: %s\n", dir)
		return idx.IndexProject(dir)
	},
}

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP server over stdio",
	RunE: func(cmd *cobra.Command, args []string) error {
		server, err := mcp.NewServer()
		if err != nil {
			return err
		}
		return server.Run()
	},
}

var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Run a natural language query",
	RunE: func(cmd *cobra.Command, args []string) error {
		q, _ := cmd.Flags().GetString("q")

		lc := llm.NewClient()
		plan, err := lc.BuildQueryPlan(q)
		if err != nil {
			return err
		}

		data, _ := json.MarshalIndent(plan, "", "  ")
		fmt.Println(string(data))

		return nil
	},
}

func init() {
	indexCmd.Flags().String("dir", ".", "Project root directory")
	queryCmd.Flags().String("q", "", "Natural language query")

	rootCmd.AddCommand(indexCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(queryCmd)
}

func Execute() error {
	return rootCmd.Execute()
}
