package tool

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

var errListFilesLimitReached = errors.New("list files limit reached")

// ListFilesDefinition is the definition for the list_files tool.
var ListFilesDefinition = Definition{
	Name:        "list_files",
	Description: "List files and directories at a given path. If no path is provided, lists files in the current directory.",
	InputSchema: ListFilesInputSchema,
	Function:    ListFiles,
}

// ListFilesInput is the input for the list_files tool.
type ListFilesInput struct {
	Path       string `json:"path,omitempty" jsonschema_description:"Optional relative path to list files from. Defaults to current directory if not provided."`
	MaxResults int    `json:"max_results,omitempty" jsonschema_description:"Maximum number of entries to return. Defaults to 200."`
}

// ListFilesInputSchema is the schema for the ListFilesInput struct.
var ListFilesInputSchema = GenerateSchema[ListFilesInput]()

// ListFiles lists files and directories at a given path.
func ListFiles(input json.RawMessage) (string, error) {
	listFilesInput := ListFilesInput{}
	err := json.Unmarshal(input, &listFilesInput)
	if err != nil {
		return "", err
	}

	dir, err := resolvePath(listFilesInput.Path)
	if err != nil {
		return "", err
	}

	maxResults := listFilesInput.MaxResults
	if maxResults <= 0 {
		maxResults = 200
	}

	skipFilter := MakeGitIgnoreFilter(dir)

	var files []string
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			if skipFilter(path, info) {
				return filepath.SkipDir
			}
		}

		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		if relPath != "." {
			relPath = filepath.ToSlash(relPath)
			if info.IsDir() {
				files = append(files, relPath+"/")
			} else {
				files = append(files, relPath)
			}

			if len(files) >= maxResults {
				return errListFilesLimitReached
			}
		}
		return nil
	})

	if err != nil && !errors.Is(err, errListFilesLimitReached) {
		return "", err
	}
	if errors.Is(err, errListFilesLimitReached) {
		files = append(files, "... truncated ...")
	}

	result, err := json.Marshal(files)
	if err != nil {
		return "", err
	}

	return string(result), nil
}
