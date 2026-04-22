package tool

import (
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
