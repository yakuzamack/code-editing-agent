package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ProjectTreeDefinition is the definition for the project_tree tool.
var ProjectTreeDefinition = Definition{
	Name:        "project_tree",
	Description: "Generate a clean, filtered directory tree of the working project. Automatically excludes noise like node_modules, vendor, .git, bin/generated-beacons, __pycache__, build artifacts, and compiled binaries. Use this to survey the project structure, understand how it is organised, and identify structural issues before proposing refactors.",
	InputSchema: GenerateSchema[ProjectTreeInput](),
	Function:    ExecuteProjectTree,
}

// ProjectTreeInput is the input for the project_tree tool.
type ProjectTreeInput struct {
	// Path is an optional subdirectory to tree (relative to workdir). Defaults to root.
	Path string `jsonschema:"description=Optional subdirectory to display (e.g. internal/server). Defaults to the project root."`
	// Depth limits how many directory levels to show. 0 means unlimited.
	Depth int `jsonschema:"description=Max directory depth to display (default 4). Set 0 for unlimited."`
	// ShowFiles includes files in the output when true (default true). Set false for dirs-only overview.
	ShowFiles bool `jsonschema:"description=Include files in the tree (default true). Set false to show directories only."`
}

// noiseDirs are directory names that are always excluded from the tree.
var noiseDirs = map[string]bool{
	"node_modules":      true,
	"vendor":            true,
	".git":              true,
	".idea":             true,
	".vscode":           true,
	"__pycache__":       true,
	"generated-beacons": true,
	"dist":              true,
	"build":             true,
	".next":             true,
	"coverage":          true,
}

// noiseFiles are file patterns that are always excluded.
var noiseFileExts = map[string]bool{
	".pyc":      true,
	".pyo":      true,
	".class":    true,
	".o":        true,
	".a":        true,
	".so":       true,
	".dylib":    true,
	".DS_Store": true,
}

// noiseFileNames are exact filenames to skip.
var noiseFileNames = map[string]bool{
	".DS_Store":     true,
	".coverage":     true,
	"server-binary": true,
}

// ExecuteProjectTree walks the project directory and renders a filtered tree.
func ExecuteProjectTree(input json.RawMessage) (string, error) {
	var args ProjectTreeInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	// Default depth
	maxDepth := 4
	if args.Depth > 0 {
		maxDepth = args.Depth
	}

	// Resolve root
	root := workingDir
	if args.Path != "" {
		resolved, err := resolvePath(args.Path)
		if err != nil {
			return "", err
		}
		root = resolved
	}

	// Try `tree` binary first — it's fast and handles many options well.
	if treeOut, ok := tryTreeBinary(root, maxDepth, args.ShowFiles || !args.ShowFiles); ok {
		return treeOut, nil
	}

	// Fallback: walk manually
	showFiles := true
	if input != nil {
		var raw map[string]interface{}
		if err := json.Unmarshal(input, &raw); err == nil {
			if sf, ok := raw["ShowFiles"]; ok {
				if b, ok := sf.(bool); ok {
					showFiles = b
				}
			}
		}
	}
	return walkTree(root, root, 0, maxDepth, showFiles)
}

// tryTreeBinary attempts to use the system `tree` command.
func tryTreeBinary(root string, depth int, showFiles bool) (string, bool) {
	path, err := exec.LookPath("tree")
	if err != nil {
		return "", false
	}

	// Build ignore pattern for tree
	ignoreList := []string{}
	for d := range noiseDirs {
		ignoreList = append(ignoreList, d)
	}
	ignorePattern := strings.Join(ignoreList, "|")

	args := []string{
		root,
		"-L", fmt.Sprintf("%d", depth),
		"--noreport",
		"-I", ignorePattern,
		"--charset", "ascii",
	}
	if !showFiles {
		args = append(args, "-d")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", false
	}
	result := string(out)
	if len(result) > 16000 {
		result = result[:16000] + "\n...[truncated — use Path and Depth to narrow scope]"
	}
	return result, true
}

// walkTree is the manual fallback tree walker.
func walkTree(root, dir string, depth, maxDepth int, showFiles bool) (string, error) {
	if maxDepth > 0 && depth > maxDepth {
		return "", nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	skipFilter := MakeGitIgnoreFilter(root)

	sort.Slice(entries, func(i, j int) bool {
		// Dirs before files, then alphabetical
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})

	var sb strings.Builder
	prefix := strings.Repeat("  ", depth)

	for _, e := range entries {
		name := e.Name()

		// Skip hidden noise
		if strings.HasPrefix(name, ".") {
			continue
		}

		absPath := filepath.Join(dir, name)

		if e.IsDir() {
			if noiseDirs[name] {
				continue
			}
			// Check .gitignore
			if skipFilter(absPath, nil) {
				continue
			}
			fmt.Fprintf(&sb, "%s%s/\n", prefix, name)
			sub, err := walkTree(root, absPath, depth+1, maxDepth, showFiles)
			if err == nil {
				sb.WriteString(sub)
			}
		} else if showFiles {
			ext := strings.ToLower(filepath.Ext(name))
			if noiseFileExts[ext] || noiseFileNames[name] {
				continue
			}
			fmt.Fprintf(&sb, "%s%s\n", prefix, name)
		}
	}

	result := sb.String()
	const walkTreeMaxBytes = 16_000
	if depth == 0 && len(result) > walkTreeMaxBytes {
		result = result[:walkTreeMaxBytes] + "\n...[truncated — use Path and Depth to narrow scope]"
	}
	return result, nil
}
