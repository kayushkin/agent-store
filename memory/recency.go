package memory

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RecentFile represents a recently modified file
type RecentFile struct {
	RelativePath string
	AbsolutePath string
	ModTime      time.Time
	Source       string // "git" or "mtime"
}

// FindRecentlyModified finds files modified within the given duration.
// Tries git first, falls back to filesystem mtime.
func FindRecentlyModified(rootDir string, since time.Duration) ([]RecentFile, error) {
	// Try git first
	gitFiles, err := findRecentlyModifiedGit(rootDir, since)
	if err == nil && len(gitFiles) > 0 {
		return gitFiles, nil
	}
	
	// Fall back to mtime
	return findRecentlyModifiedMtime(rootDir, since)
}

// findRecentlyModifiedGit uses git to find recently modified files
func findRecentlyModifiedGit(rootDir string, since time.Duration) ([]RecentFile, error) {
	// Check if we're in a git repo
	gitDir := filepath.Join(rootDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return nil, err
	}
	
	// Git command to find files modified in the last N seconds
	sinceTime := time.Now().Add(-since)
	sinceArg := sinceTime.Format("2006-01-02 15:04:05")
	
	cmd := exec.Command("git", "log", "--pretty=format:", "--name-only", "--since", sinceArg)
	cmd.Dir = rootDir
	
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	
	// Parse output and deduplicate
	fileMap := make(map[string]bool)
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			fileMap[line] = true
		}
	}
	
	// Get actual file mod times
	var results []RecentFile
	for relPath := range fileMap {
		fullPath := filepath.Join(rootDir, relPath)
		info, err := os.Stat(fullPath)
		if err != nil {
			continue // File might have been deleted
		}
		
		// Only include if still within the time window
		if time.Since(info.ModTime()) <= since {
			results = append(results, RecentFile{
				RelativePath: relPath,
				AbsolutePath: fullPath,
				ModTime:      info.ModTime(),
				Source:       "git",
			})
		}
	}
	
	return results, nil
}

// findRecentlyModifiedMtime scans filesystem for recently modified files
func findRecentlyModifiedMtime(rootDir string, since time.Duration) ([]RecentFile, error) {
	cutoff := time.Now().Add(-since)
	var results []RecentFile
	
	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		
		// Skip directories
		if info.IsDir() {
			// Skip common ignore patterns
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" || base == "vendor" || base == ".openclaw" || base == ".inber" || base == "logs" || base == "test-results" {
				return filepath.SkipDir
			}
			return nil
		}
		
		// Check if modified recently
		if info.ModTime().After(cutoff) {
			relPath, err := filepath.Rel(rootDir, path)
			if err != nil {
				relPath = path
			}
			
			results = append(results, RecentFile{
				RelativePath: relPath,
				AbsolutePath: path,
				ModTime:      info.ModTime(),
				Source:       "mtime",
			})
		}
		
		return nil
	})
	
	return results, err
}

// FormatRecentFiles formats recent files into a human-readable string
func FormatRecentFiles(files []RecentFile) string {
	if len(files) == 0 {
		return "No recently modified files."
	}
	
	var builder strings.Builder
	builder.WriteString("# Recently Modified Files\n\n")
	
	for _, file := range files {
		timeSince := time.Since(file.ModTime)
		var timeStr string
		
		if timeSince < time.Minute {
			timeStr = "just now"
		} else if timeSince < time.Hour {
			mins := int(timeSince.Minutes())
			timeStr = fmt.Sprintf("%d minute%s ago", mins, plural(mins))
		} else if timeSince < 24*time.Hour {
			hours := int(timeSince.Hours())
			timeStr = fmt.Sprintf("%d hour%s ago", hours, plural(hours))
		} else {
			days := int(timeSince.Hours() / 24)
			timeStr = fmt.Sprintf("%d day%s ago", days, plural(days))
		}
		
		builder.WriteString(fmt.Sprintf("- %s (%s, via %s)\n", file.RelativePath, timeStr, file.Source))
	}
	
	return builder.String()
}

// plural returns "s" if count != 1, empty string otherwise
func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}