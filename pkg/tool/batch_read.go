package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// BatchReadDefinition is the definition for the batch_read tool.
var BatchReadDefinition = Definition{
	Name: "batch_read",
	Description: `Read multiple files in parallel to avoid sequential slowdowns. Returns a map of file paths to contents.
Useful when you need to read many files at once (e.g., understanding module structure, comparing implementations).
Limits: max 20 files per call, timeout 30s per file.`,
	InputSchema: GenerateSchema[BatchReadInput](),
	Function:    ExecuteBatchRead,
}

// BatchReadInput is the input for the batch_read tool.
type BatchReadInput struct {
	// Files is a list of relative or absolute file paths to read
	Files []string `json:"files" jsonschema:"description=List of file paths (relative to workdir) to read in parallel. Max 20 files."`
	// MaxBytes limits each file to N bytes (default 50KB, max 500KB)
	MaxBytes int `json:"max_bytes,omitempty" jsonschema:"description=Max bytes per file (default 50000, max 500000)"`
}

// ExecuteBatchRead reads multiple files in parallel.
func ExecuteBatchRead(input json.RawMessage) (string, error) {
	var args BatchReadInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	if len(args.Files) == 0 {
		return "", fmt.Errorf("files list is required")
	}
	if len(args.Files) > 20 {
		return "", fmt.Errorf("max 20 files per call, got %d", len(args.Files))
	}

	maxBytes := args.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 50000
	}
	if maxBytes > 500000 {
		maxBytes = 500000
	}

	// Parallel reads using goroutines
	type fileResult struct {
		path    string
		content string
		err     string
	}

	results := make([]fileResult, len(args.Files))
	var wg sync.WaitGroup

	for i, fp := range args.Files {
		wg.Add(1)
		go func(idx int, fp string) {
			defer wg.Done()

			// Resolve path relative to workdir
			absPath := fp
			if !filepath.IsAbs(fp) {
				absPath = filepath.Join(workingDir, fp)
			}

			data, err := os.ReadFile(absPath)
			if err != nil {
				results[idx].path = fp
				results[idx].err = err.Error()
				return
			}

			// Truncate if too large
			if len(data) > maxBytes {
				results[idx].content = string(data[:maxBytes]) + fmt.Sprintf("\n\n[... truncated %d bytes ...]", len(data)-maxBytes)
			} else {
				results[idx].content = string(data)
			}
			results[idx].path = fp
		}(i, fp)
	}

	wg.Wait()

	// Format results
	var output strings.Builder
	successCount := 0
	for _, r := range results {
		if r.err != "" {
			fmt.Fprintf(&output, "❌ %s — %s\n\n", r.path, r.err)
		} else {
			fmt.Fprintf(&output, "✅ %s\n\n```\n%s\n```\n\n", r.path, r.content)
			successCount++
		}
	}

	fmt.Fprintf(&output, "---\n**Read %d/%d files successfully** (parallel batch mode)\n", successCount, len(args.Files))
	return output.String(), nil
}
