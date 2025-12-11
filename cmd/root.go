package cmd

import (
	"codebase/internal/config"
	"codebase/internal/embeddings"
	"codebase/internal/indexer"
	"codebase/internal/mcp"
	"codebase/internal/parser"
	"codebase/internal/qdrant"
	"codebase/internal/updater"
	"codebase/internal/utils"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// These variables are set during build using ldflags
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
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
		// Load shared config (~/.codebase/config.json) so OPENAI_*/QDRANT_*
		// from that file are visible as env vars when running via CLI.
		if err := config.LoadFromUserConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		}

		dir, _ := cmd.Flags().GetString("dir")

		qc, err := qdrant.NewClient()
		if err != nil {
			return err
		}
		defer qc.Close()

		ec := embeddings.NewClient()
		idx := indexer.NewIndexer(qc, ec)

		// Register language parsers so that source files can actually be
		// parsed into function-level chunks before indexing.
		idx.RegisterParser(string(parser.LanguageGo), parser.NewGoParser())
		idx.RegisterParser(string(parser.LanguagePython), parser.NewPythonParser())
		idx.RegisterParser(string(parser.LanguageJavaScript), parser.NewJavaScriptParser())
		idx.RegisterParser(string(parser.LanguageTypeScript), parser.NewTypeScriptParser())

		fmt.Printf("Indexing project at: %s\n", dir)
		return idx.IndexProject(dir)
	},
}

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP server over stdio",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("dir")
		server, err := mcp.NewServer(dir)
		if err != nil {
			return err
		}
		return server.Run()
	},
}

var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Run a natural language semantic code search (same as MCP codebase-retrieval)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Ensure the same config file is loaded for query as well, so
		// LLM client picks up OPENAI_* settings from ~/.codebase/config.json.
		if err := config.LoadFromUserConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		}

		q, _ := cmd.Flags().GetString("q")
		topK, _ := cmd.Flags().GetInt("top_k")
		dir, _ := cmd.Flags().GetString("dir")
		if topK <= 0 {
			topK = 10
		}

		// Create MCP server to reuse the same search logic
		server, err := mcp.NewServer(dir)
		if err != nil {
			return err
		}
		defer server.Close()

		// Use the same search logic as MCP
		queryArgs := map[string]interface{}{
			"query":        q,
			"top_k":        topK,
			"project_path": dir,
		}
		argsJSON, _ := json.Marshal(queryArgs)

		result, err := server.HandleCodebaseRetrieval(argsJSON)
		if err != nil {
			return err
		}

		// Format output consistently
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))

		return nil
	},
}

var clearIndexCmd = &cobra.Command{
	Use:   "clear-index",
	Short: "Delete the entire Qdrant collection used for codebase index",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load shared config so QDRANT_* settings from ~/.codebase/config.json
		// are available when clearing the index.
		if err := config.LoadFromUserConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		}

		dir, _ := cmd.Flags().GetString("dir")
		projectID, err := utils.ComputeProjectID(dir)
		if err != nil {
			return fmt.Errorf("failed to compute project id: %w", err)
		}
		collection := indexer.CollectionName(projectID)

		qc, err := qdrant.NewClient()
		if err != nil {
			return err
		}
		defer qc.Close()

		fmt.Printf("Deleting collection: %s\n", collection)
		if err := qc.DeleteCollection(collection); err != nil {
			return err
		}
		fmt.Println("✓ Collection deleted")
		return nil
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("codebase version %s\n", Version)
		fmt.Printf("Git commit: %s\n", GitCommit)
		fmt.Printf("Build time: %s\n", BuildTime)
	},
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update codebase to the latest version",
	Long:  "Check for and install the latest version of codebase from GitHub releases",
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		checkOnly, _ := cmd.Flags().GetBool("check")

		u := updater.NewUpdater(Version)

		fmt.Println("Checking for updates...")
		release, hasUpdate, err := u.CheckForUpdate()
		if err != nil {
			return fmt.Errorf("failed to check for updates: %w", err)
		}

		if !hasUpdate && !force {
			fmt.Println("You are already running the latest version.")
			return nil
		}

		fmt.Printf("Current version: %s\n", Version)
		fmt.Printf("Latest version:  %s\n", release.TagName)

		if checkOnly {
			if hasUpdate {
				fmt.Println("\nA new version is available!")
				fmt.Println("Run 'codebase update' to install it.")
			}
			return nil
		}

		if hasUpdate || force {
			fmt.Println("\nDownloading and installing update...")
			if err := u.Update(release); err != nil {
				return fmt.Errorf("update failed: %w", err)
			}
			fmt.Printf("\nSuccessfully updated to version %s\n", release.TagName)
		}

		return nil
	},
}

func init() {
	indexCmd.Flags().String("dir", ".", "Project root directory")
	queryCmd.Flags().String("q", "", "Natural language query")
	queryCmd.Flags().Int("top_k", 10, "Maximum number of results to return")
	queryCmd.Flags().String("dir", ".", "Project root directory (must match the directory passed to 'codebase index')")
	mcpCmd.Flags().String("dir", ".", "Project root directory (server scopes searches to this directory)")
	clearIndexCmd.Flags().String("dir", ".", "Project root directory to clear from Qdrant")

	updateCmd.Flags().Bool("check", false, "Check for updates without installing")
	updateCmd.Flags().Bool("force", false, "Force update even if already on latest version")

	rootCmd.AddCommand(indexCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(queryCmd)
	rootCmd.AddCommand(clearIndexCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(updateCmd)
}

func Execute() error {
	return rootCmd.Execute()
}
