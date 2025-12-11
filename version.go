package main

// Version is the current version of the codebase CLI tool
// This will be overridden during build with -ldflags "-X main.Version=x.x.x"
var Version = "dev"

// GitCommit is the git commit hash
var GitCommit = "unknown"

// BuildTime is the build timestamp
var BuildTime = "unknown"
