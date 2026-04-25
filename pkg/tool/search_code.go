package tool

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var errSearchLimitReached = errors.New("search limit reached")

// SearchCodeDefinition is the definition for the search_code tool.
var SearchCodeDefinition = Definition{
	Name:        "search_code",
	Description: "Search for text across files in the working directory and return matching lines.",
	InputSchema: SearchCodeInputSchema,
	Function:    SearchCode,
}

// SearchCodeInput is the input for the search_code tool.
type SearchCodeInput struct {
	Query            string `json:"query" jsonschema_description:"Text to search for."`
	Path             string `json:"path,omitempty" jsonschema_description:"Optional relative path to search from. Defaults to the working directory."`
	CaseSensitive    bool   `json:"case_sensitive,omitempty" jsonschema_description:"Whether the search should be case-sensitive. Defaults to false."`
	MaxResults       int    `json:"max_results,omitempty" jsonschema_description:"Maximum number of matches to return. Defaults to 20."`
	MaxFileSizeMB    int    `json:"max_file_size_mb,omitempty" jsonschema_description:"Skip files larger than this (in MB). Defaults to 10 MB."`
	ExcludeGitIgnore bool   `json:"exclude_gitignore,omitempty" jsonschema_description:"If true, parse .gitignore rules and skip ignored paths (default true)."`
}

// SearchCodeMatch is a single search result.
type SearchCodeMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// SearchCodeInputSchema is the schema for the SearchCodeInput struct.
var SearchCodeInputSchema = GenerateSchema[SearchCodeInput]()

// SearchCode searches for text across files in the configured working directory.
func SearchCode(input json.RawMessage) (string, error) {
	searchCodeInput := SearchCodeInput{}
	err := json.Unmarshal(input, &searchCodeInput)
	if err != nil {
		return "", err
	}
	if searchCodeInput.Query == "" {
		return "", errors.New("query is required")
	}

	rootDir, err := resolvePath(searchCodeInput.Path)
	if err != nil {
		return "", err
	}

	maxResults := searchCodeInput.MaxResults
	if maxResults <= 0 {
		maxResults = 20
	}

	// Default: skip files > 10 MB
	maxFileSize := int64(searchCodeInput.MaxFileSizeMB) * 1024 * 1024
	if maxFileSize <= 0 {
		maxFileSize = 10 * 1024 * 1024 // 10 MB default
	}

	excludeGitIgnore := searchCodeInput.ExcludeGitIgnore || !searchCodeInput.CaseSensitive // default true

	needle := searchCodeInput.Query
	if !searchCodeInput.CaseSensitive {
		needle = strings.ToLower(needle)
	}

	// Create .gitignore-aware skip filter
	var skipFilter WalkSkipFunc
	if excludeGitIgnore {
		skipFilter = MakeGitIgnoreFilter(rootDir)
	}

	matches := make([]SearchCodeMatch, 0, maxResults)
	err = filepath.Walk(rootDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if info.IsDir() {
			if excludeGitIgnore && skipFilter(path, info) {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		// Skip large files
		if info.Size() > maxFileSize {
			return nil
		}

		isBinary, err := isBinaryFile(path)
		if err != nil {
			return err
		}
		if isBinary {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() {
			if err := file.Close(); err != nil {
				fmt.Printf("Warning: failed to close file %s: %v\n", path, err)
			}
		}()

		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := scanner.Text()
			haystack := line
			if !searchCodeInput.CaseSensitive {
				haystack = strings.ToLower(line)
			}

			if strings.Contains(haystack, needle) {
				relPath, err := filepath.Rel(WorkingDir(), path)
				if err != nil {
					return err
				}

				matches = append(matches, SearchCodeMatch{
					Path:    filepath.ToSlash(relPath),
					Line:    lineNumber,
					Content: strings.TrimSpace(line),
				})

				if len(matches) >= maxResults {
					return errSearchLimitReached
				}
			}
		}

		if err := scanner.Err(); err != nil {
			return err
		}

		return nil
	})
	if err != nil && !errors.Is(err, errSearchLimitReached) {
		return "", err
	}

	payload, err := json.Marshal(matches)
	if err != nil {
		return "", err
	}

	return string(payload), nil
}

func isBinaryFile(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			fmt.Printf("Warning: failed to close file %s: %v\n", path, err)
		}
	}()

	buffer := make([]byte, 8000)
	bytesRead, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}

	return bytes.IndexByte(buffer[:bytesRead], 0) >= 0, nil
}
