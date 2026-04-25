package tool

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var workingDir string

// SetWorkingDir configures the project root used by all file and command tools.
func SetWorkingDir(dir string) error {
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = cwd
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	info, err := os.Stat(absDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("working directory is not a directory: %s", absDir)
	}

	workingDir = absDir
	return nil
}

// WorkingDir returns the configured project root.
func WorkingDir() string {
	if workingDir != "" {
		return workingDir
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func resolvePath(userPath string) (string, error) {
	baseDir := WorkingDir()
	if userPath == "" {
		return baseDir, nil
	}

	var candidate string
	if filepath.IsAbs(userPath) {
		candidate = filepath.Clean(userPath)
	} else {
		candidate = filepath.Join(baseDir, userPath)
	}

	absPath, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}

	relPath, err := filepath.Rel(baseDir, absPath)
	if err != nil {
		return "", err
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path is outside the working directory: %s", userPath)
	}

	return absPath, nil
}

func displayPath(absPath string) string {
	relPath, err := filepath.Rel(WorkingDir(), absPath)
	if err != nil || relPath == "." {
		return absPath
	}

	return filepath.ToSlash(relPath)
}

// --- .gitignore-aware file walker ---

// gitIgnoreMatcher holds parsed .gitignore patterns for a directory.
type gitIgnoreMatcher struct {
	patterns []gitIgnorePattern
}

type gitIgnorePattern struct {
	pattern string
	negate  bool
	dirOnly bool // if pattern ends with /
}

// loadGitIgnore reads and parses a .gitignore file.
func loadGitIgnore(dir string) *gitIgnoreMatcher {
	path := filepath.Join(dir, ".gitignore")
	f, err := os.Open(path)
	if err != nil {
		return &gitIgnoreMatcher{} // no .gitignore
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			// Log close error
			_ = closeErr
		}
	}()

	var m gitIgnoreMatcher
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		negate := false
		if strings.HasPrefix(line, "!") {
			negate = true
			line = line[1:]
		}

		dirOnly := strings.HasSuffix(line, "/")
		if dirOnly {
			line = strings.TrimSuffix(line, "/")
		}

		// Remove leading slash (anchored to repo root)
		line = strings.TrimPrefix(line, "/")

		m.patterns = append(m.patterns, gitIgnorePattern{
			pattern: line,
			negate:  negate,
			dirOnly: dirOnly,
		})
	}
	return &m
}

// isIgnored checks if a relative path (from repo root) matches any .gitignore pattern.
func (m *gitIgnoreMatcher) isIgnored(relPath string, isDir bool) bool {
	if len(m.patterns) == 0 {
		return false
	}

	// Normalize separators
	relPath = filepath.ToSlash(relPath)

	matched := false
	for _, p := range m.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if matchGitIgnorePattern(relPath, p.pattern) {
			matched = !p.negate
		}
	}
	return matched
}

// matchGitIgnorePattern checks if a path matches a single gitignore pattern.
func matchGitIgnorePattern(path, pattern string) bool {
	// Simple glob matching: supports **, *, and ?
	// Pattern matches basename (e.g., "bin") or full subpath (e.g., "web/dist")
	if strings.Contains(pattern, "/") {
		// Multi-segment pattern — match against full relative path
		return globMatch(path, pattern)
	}
	// Single-segment pattern — match against any component of the path
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if globMatch(part, pattern) {
			return true
		}
	}
	return false
}

// globMatch checks if name matches a simple glob pattern.
func globMatch(name, pattern string) bool {
	// Convert simple glob to matching logic
	pi := 0
	ni := 0
	nextP := -1
	nextN := -1

	for ni < len(name) {
		if pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == name[ni]) {
			pi++
			ni++
		} else if pi < len(pattern) && pattern[pi] == '*' {
			nextP = pi
			nextN = ni + 1
			pi++
		} else if nextP != -1 {
			pi = nextP
			ni = nextN
			nextP = -1
		} else {
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

// gitIgnoreFilter returns a WalkSkipFunc that respects .gitignore patterns.
// walkDir is the absolute root directory being walked; skipFunc returns true
// for directories that should not be entered.
type WalkSkipFunc func(absPath string, info os.FileInfo) bool

// MakeGitIgnoreFilter creates a WalkSkipFunc that respects .gitignore.
// It loads .gitignore files from each directory as it descends.
func MakeGitIgnoreFilter(walkRoot string) WalkSkipFunc {
	cache := make(map[string]*gitIgnoreMatcher)

	fn := func(absPath string, info os.FileInfo) bool {
		if !info.IsDir() {
			return false
		}

		rel, err := filepath.Rel(walkRoot, absPath)
		if err != nil {
			return true
		}
		rel = filepath.ToSlash(rel)

		name := info.Name()

		// Always skip these regardless of .gitignore
		if name == ".git" || name == "node_modules" {
			return true
		}

		// Check parent's .gitignore for this directory
		parent := filepath.Dir(absPath)
		gm, ok := cache[parent]
		if !ok {
			gm = loadGitIgnore(parent)
			cache[parent] = gm
		}
		if gm.isIgnored(rel, true) {
			return true
		}

		// Load this dir's own .gitignore for children
		if _, ok := cache[absPath]; !ok {
			cache[absPath] = loadGitIgnore(absPath)
		}

		return false
	}
	return fn
}
