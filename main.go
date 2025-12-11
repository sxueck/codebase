package main

import (
	"codebase/cmd"
	"codebase/internal/updater"
	"fmt"
	"os"
)

func main() {
	// Clean up old version backup (Windows only)
	// This silently removes the .old file left by previous updates
	updater.CleanupOldVersion()

	// Pass version info to cmd package
	cmd.Version = Version
	cmd.GitCommit = GitCommit
	cmd.BuildTime = BuildTime

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}