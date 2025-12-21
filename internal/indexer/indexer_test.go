package indexer

import (
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codebase/internal/utils"
)

func TestCollectionName(t *testing.T) {
	t.Parallel()

	if got := CollectionName(""); got != defaultCollectionName {
		t.Fatalf("CollectionName(\"\")=%q, want %q", got, defaultCollectionName)
	}
	if got := CollectionName("   \t\n"); got != defaultCollectionName {
		t.Fatalf("CollectionName(whitespace)=%q, want %q", got, defaultCollectionName)
	}
	if got := CollectionName("  abc  "); got != collectionPrefix+"abc" {
		t.Fatalf("CollectionName(\"  abc  \" )=%q, want %q", got, collectionPrefix+"abc")
	}
}

func TestContentHashToPointID(t *testing.T) {
	t.Parallel()

	hash := "deadbeef"
	got := contentHashToPointID(hash)

	h := sha256.Sum256([]byte(hash))
	want := binary.BigEndian.Uint64(h[:8])
	if got != want {
		t.Fatalf("contentHashToPointID=%d, want %d", got, want)
	}

	if got2 := contentHashToPointID(hash); got2 != got {
		t.Fatalf("contentHashToPointID not deterministic: %d vs %d", got2, got)
	}
}

func TestHashFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	content := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := hashFile(path)
	if err != nil {
		t.Fatalf("hashFile: %v", err)
	}
	want := utils.HashContent(content)
	if got != want {
		t.Fatalf("hashFile=%q, want %q", got, want)
	}

	if _, err := hashFile(filepath.Join(dir, "missing.txt")); err == nil {
		t.Fatalf("hashFile(missing) expected error")
	}
}

func TestNormalizeFilePath(t *testing.T) {
	t.Parallel()

	if got := normalizeFilePath("  "); got != "" {
		t.Fatalf("normalizeFilePath(whitespace)=%q, want empty", got)
	}

	dir := t.TempDir()
	absInput := filepath.Join(dir, "a", "..", "b", "file.go")

	gotAbs := normalizeFilePath(absInput)
	expectedAbs := filepath.ToSlash(filepath.Clean(absInput))
	if runtime.GOOS == "windows" {
		expectedAbs = strings.ToLower(expectedAbs)
	}
	if gotAbs != expectedAbs {
		t.Fatalf("normalizeFilePath(abs)=%q, want %q", gotAbs, expectedAbs)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	relInput := filepath.Join(".", "rel", "..", "rel", "file.go")
	relAbs := filepath.Join(wd, "rel", "file.go")
	gotRel := normalizeFilePath(relInput)
	expectedRel := filepath.ToSlash(filepath.Clean(relAbs))
	if runtime.GOOS == "windows" {
		expectedRel = strings.ToLower(expectedRel)
	}
	if gotRel != expectedRel {
		t.Fatalf("normalizeFilePath(rel)=%q, want %q", gotRel, expectedRel)
	}
}

func TestCanonicalizeHashKeys(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	normalizedRoot, err := utils.NormalizeProjectRoot(root)
	if err != nil {
		t.Fatalf("NormalizeProjectRoot: %v", err)
	}

	hashes := map[string]string{
		"./foo/bar.go": "h1",
		"foo/baz.go":  "h2",
		"":            "ignored",
		"   ":         "ignored",
	}
	got := canonicalizeHashKeys(hashes, normalizedRoot)
	if len(got) != 2 {
		t.Fatalf("canonicalizeHashKeys len=%d, want 2", len(got))
	}

	path1 := normalizeFilePath(filepath.Join(normalizedRoot, filepath.FromSlash("foo/bar.go")))
	if got[path1] != "h1" {
		t.Fatalf("canonicalizeHashKeys[%q]=%q, want %q", path1, got[path1], "h1")
	}
	path2 := normalizeFilePath(filepath.Join(normalizedRoot, filepath.FromSlash("foo/baz.go")))
	if got[path2] != "h2" {
		t.Fatalf("canonicalizeHashKeys[%q]=%q, want %q", path2, got[path2], "h2")
	}
}

func TestFileHashStateLifecycle(t *testing.T) {
	// This test sets HOME/USERPROFILE, so do not run in parallel.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	projectID := "project123"

	// Missing state file should return an empty map (not nil).
	loaded, err := loadFileHashes(projectID)
	if err != nil {
		t.Fatalf("loadFileHashes (missing): %v", err)
	}
	if loaded == nil {
		t.Fatalf("loadFileHashes returned nil map")
	}
	if len(loaded) != 0 {
		t.Fatalf("loadFileHashes (missing) len=%d, want 0", len(loaded))
	}

	statePath, err := fileHashStatePath(projectID)
	if err != nil {
		t.Fatalf("fileHashStatePath: %v", err)
	}
	if base := filepath.Base(statePath); base != projectID+"_file_hashes.json" {
		t.Fatalf("state file base=%q, want %q", base, projectID+"_file_hashes.json")
	}
	if parent := filepath.Base(filepath.Dir(statePath)); parent != ".codebase" {
		t.Fatalf("state file dir base=%q, want %q", parent, ".codebase")
	}

	hashes := map[string]string{"/abs/path/file.go": "hash"}
	if err := saveFileHashes(projectID, hashes); err != nil {
		t.Fatalf("saveFileHashes: %v", err)
	}

	loaded, err = loadFileHashes(projectID)
	if err != nil {
		t.Fatalf("loadFileHashes: %v", err)
	}
	if loaded["/abs/path/file.go"] != "hash" {
		t.Fatalf("loaded hash=%q, want %q", loaded["/abs/path/file.go"], "hash")
	}

	if err := ClearProjectState(projectID); err != nil {
		t.Fatalf("ClearProjectState: %v", err)
	}
	if _, err := os.Stat(statePath); err == nil {
		t.Fatalf("expected state file to be removed")
	}

	// Clearing again should be a no-op.
	if err := ClearProjectState(projectID); err != nil {
		t.Fatalf("ClearProjectState (missing): %v", err)
	}
}

func TestFileHashStatePathDefaultProjectID(t *testing.T) {
	// This test sets HOME/USERPROFILE, so do not run in parallel.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	statePath, err := fileHashStatePath("")
	if err != nil {
		t.Fatalf("fileHashStatePath: %v", err)
	}
	if base := filepath.Base(statePath); base != "default_file_hashes.json" {
		t.Fatalf("state file base=%q, want %q", base, "default_file_hashes.json")
	}
}

