package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var excludedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	".next":        true,
	"__pycache__":  true,
	".venv":        true,
}

var languageExts = map[string]string{
	".go":   "go",
	".py":   "python",
	".ts":   "typescript",
	".tsx":  "typescript",
	".js":   "javascript",
	".jsx":  "javascript",
}

func GetAllSourceFiles(rootPath string) ([]string, error) {
	var files []string
	ignorePatterns := loadGitIgnorePatterns(rootPath)
	err := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Compute path relative to root for .gitignore-style matching.
		relPath, relErr := filepath.Rel(rootPath, path)
		if relErr != nil {
			relPath = path
		}
		relPath = filepath.ToSlash(relPath)

		if d.IsDir() {
			// Always skip well-known heavy/irrelevant directories.
			if excludedDirs[d.Name()] {
				return filepath.SkipDir
			}
			// Respect top-level .gitignore rules for directories.
			if isIgnoredPath(relPath, ignorePatterns) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip files that match .gitignore-style patterns.
		if isIgnoredPath(relPath, ignorePatterns) {
			return nil
		}

		ext := filepath.Ext(path)
		if _, ok := languageExts[ext]; ok {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func DetectLanguage(path string) string {
	ext := filepath.Ext(path)
	return languageExts[ext]
}

func HashContent(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

func CosineSim(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dotProduct / (normA * normB)
}

func NormalizeQuery(query string) string {
	return strings.TrimSpace(query)
}

// loadGitIgnorePatterns reads the root-level .gitignore (if present) and
// returns a list of non-empty, non-comment patterns.
func loadGitIgnorePatterns(rootPath string) []string {
	gitIgnorePath := filepath.Join(rootPath, ".gitignore")
	data, err := os.ReadFile(gitIgnorePath)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	var patterns []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// isIgnoredPath applies a minimal subset of .gitignore semantics suitable for
// skipping heavy directories like node_modules/ and common file patterns. It
// treats patterns as root-relative against the provided relPath.
func isIgnoredPath(relPath string, patterns []string) bool {
	relPath = strings.TrimPrefix(relPath, "./")
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return false
	}

	relPath = filepath.ToSlash(relPath)

	for _, pattern := range patterns {
		p := strings.TrimSpace(pattern)
		if p == "" {
			continue
		}

		p = filepath.ToSlash(p)

		// Directory-style pattern, e.g. "node_modules/".
		if strings.HasSuffix(p, "/") {
			dir := strings.TrimSuffix(p, "/")
			dir = strings.TrimPrefix(dir, "./")
			if relPath == dir || strings.HasPrefix(relPath, dir+"/") {
				return true
			}
			continue
		}

		// Use filepath.Match for glob-style patterns.
		if ok, _ := filepath.Match(p, relPath); ok {
			return true
		}

		// Bare name pattern like "node_modules" or "dist" without slashes or
		// wildcards – treat as directory segment match anywhere in the path.
		if !strings.Contains(p, "/") && !strings.ContainsAny(p, "*?[") {
			segment := "/" + p + "/"
			if strings.Contains("/"+relPath+"/", segment) {
				return true
			}
		}
	}

	return false
}
