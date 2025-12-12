package updater

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	githubAPIURL = "https://api.github.com/repos/sxueck/codebase/releases/latest"
	userAgent    = "codebase-updater"
)

// Release represents a GitHub release
type Release struct {
	TagName string  `json:"tag_name"`
	Name    string  `json:"name"`
	Assets  []Asset `json:"assets"`
}

// Asset represents a release asset
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// Updater handles self-update logic
type Updater struct {
	currentVersion string
	owner          string
	repo           string
	mirror         string
}

// NewUpdater creates a new updater instance
func NewUpdater(currentVersion, mirror string) *Updater {
	return &Updater{
		currentVersion: currentVersion,
		owner:          "sxueck",
		repo:           "codebase",
		mirror:         strings.TrimRight(mirror, "/"),
	}
}

// withMirror prefixes the given URL with the configured mirror if present.
// For example:
//   mirror: https://proxy.example.com
//   url:    https://api.github.com/...
// Result:
//   https://proxy.example.com/https://api.github.com/...
func (u *Updater) withMirror(url string) string {
	if u.mirror == "" {
		return url
	}
	return u.mirror + "/" + url
}

// CheckForUpdate checks if a new version is available
func (u *Updater) CheckForUpdate() (*Release, bool, error) {
	release, err := u.getLatestRelease()
	if err != nil {
		return nil, false, fmt.Errorf("failed to fetch latest release: %w", err)
	}

	if u.currentVersion == "dev" {
		return release, true, nil
	}

	// Compare versions (simple string comparison for now)
	latestVersion := strings.TrimPrefix(release.TagName, "v")
	currentVersion := strings.TrimPrefix(u.currentVersion, "v")

	if latestVersion != currentVersion {
		return release, true, nil
	}

	return release, false, nil
}

// getLatestRelease fetches the latest release from GitHub API
func (u *Updater) getLatestRelease() (*Release, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequest("GET", u.withMirror(githubAPIURL), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned status: %d", resp.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	return &release, nil
}

// selectAsset selects the appropriate asset for the current platform
func (u *Updater) selectAsset(assets []Asset) (*Asset, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Look for matching asset
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)

		// Check if it matches the OS and architecture
		if strings.Contains(name, goos) {
			// For simple name matching
			if strings.Contains(name, goarch) ||
			   (goarch == "amd64" && (strings.Contains(name, "x86_64") || strings.Contains(name, "x64"))) ||
			   (goarch == "386" && strings.Contains(name, "x86")) {
				return &asset, nil
			}
		}
	}

	// Fallback: try to find any asset that matches the OS
	for _, asset := range assets {
		if strings.Contains(strings.ToLower(asset.Name), goos) {
			return &asset, nil
		}
	}

	return nil, fmt.Errorf("no suitable asset found for %s/%s", goos, goarch)
}

// Update performs the update by downloading and replacing the current binary
func (u *Updater) Update(release *Release) error {
	// Select appropriate asset
	asset, err := u.selectAsset(release.Assets)
	if err != nil {
		return err
	}

	fmt.Printf("Downloading %s (%d bytes)...\n", asset.Name, asset.Size)

	// Download the asset
	tmpFile, err := u.downloadAsset(asset)
	if err != nil {
		return fmt.Errorf("failed to download asset: %w", err)
	}
	defer os.Remove(tmpFile)

	// Get current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Handle extraction and replacement based on file type
	ext := strings.ToLower(filepath.Ext(asset.Name))
	if ext == ".zip" {
		return u.updateFromZip(tmpFile, execPath)
	} else {
		return u.updateFromBinary(tmpFile, execPath)
	}
}

// downloadAsset downloads a release asset to a temporary file
func (u *Updater) downloadAsset(asset *Asset) (string, error) {
	client := &http.Client{
		Timeout: 5 * time.Minute,
	}

	req, err := http.NewRequest("GET", u.withMirror(asset.BrowserDownloadURL), nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "codebase-update-*")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	// Download with progress
	_, err = io.Copy(tmpFile, resp.Body)
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}

	return tmpFile.Name(), nil
}

// updateFromZip extracts the binary from a zip file and replaces the current executable
func (u *Updater) updateFromZip(zipPath, execPath string) error {
	// Open the zip file
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	// Find the executable in the zip
	var binaryFile *zip.File
	execName := filepath.Base(execPath)

	for _, f := range r.File {
		name := filepath.Base(f.Name)
		// Look for codebase.exe or codebase
		if name == execName ||
		   strings.HasPrefix(name, "codebase") &&
		   (strings.HasSuffix(name, ".exe") || !strings.Contains(name, ".")) {
			binaryFile = f
			break
		}
	}

	if binaryFile == nil {
		return fmt.Errorf("executable not found in zip archive")
	}

	// Extract to temporary file
	rc, err := binaryFile.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	tmpBinary, err := os.CreateTemp("", "codebase-binary-*")
	if err != nil {
		return err
	}
	tmpBinaryPath := tmpBinary.Name()
	defer os.Remove(tmpBinaryPath)

	_, err = io.Copy(tmpBinary, rc)
	tmpBinary.Close()
	if err != nil {
		return err
	}

	return u.replaceExecutable(tmpBinaryPath, execPath)
}

// updateFromBinary replaces the current executable with a new binary
func (u *Updater) updateFromBinary(newBinaryPath, execPath string) error {
	return u.replaceExecutable(newBinaryPath, execPath)
}

// replaceExecutable replaces the current executable with a new one
// On Windows, this requires special handling because you can't replace a running executable
func (u *Updater) replaceExecutable(newPath, execPath string) error {
	// Make the new binary executable (Unix-like systems)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(newPath, 0755); err != nil {
			return fmt.Errorf("failed to make binary executable: %w", err)
		}
	}

	if runtime.GOOS == "windows" {
		// On Windows, we need to:
		// 1. Rename the current executable to .old
		// 2. Copy the new executable to the original location
		// 3. The .old file will be deleted on next run

		oldPath := execPath + ".old"

		// Remove any existing .old file
		os.Remove(oldPath)

		// Rename current executable
		if err := os.Rename(execPath, oldPath); err != nil {
			return fmt.Errorf("failed to backup current executable: %w", err)
		}

		// Copy new executable
		if err := copyFile(newPath, execPath); err != nil {
			// Try to restore the old executable
			os.Rename(oldPath, execPath)
			return fmt.Errorf("failed to copy new executable: %w", err)
		}

		fmt.Println("Update successful! The old version will be removed on next run.")
		fmt.Println("Please restart the application to use the new version.")

	} else {
		// On Unix-like systems, we can replace the executable directly
		if err := os.Rename(newPath, execPath); err != nil {
			return fmt.Errorf("failed to replace executable: %w", err)
		}

		fmt.Println("Update successful!")
	}

	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// CleanupOldVersion removes the old executable backup (Windows only)
func CleanupOldVersion() error {
	if runtime.GOOS != "windows" {
		return nil
	}

	execPath, err := os.Executable()
	if err != nil {
		return nil
	}

	oldPath := execPath + ".old"
	if _, err := os.Stat(oldPath); err == nil {
		// Old file exists, try to remove it
		os.Remove(oldPath)
	}

	return nil
}
