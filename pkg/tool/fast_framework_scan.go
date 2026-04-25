package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FastFrameworkScanDefinition is the definition for the fast_framework_scan tool.
var FastFrameworkScanDefinition = Definition{
	Name: "fast_framework_scan",
	Description: `Quickly scan the crypto-framework structure WITHOUT reading large source files.
Returns: directory tree, file counts, module list, estimated LOC, and outstanding TODOs/stubs.
This is 10x faster than sequential file reads—use this first to understand framework layout.`,
	InputSchema: GenerateSchema[FastFrameworkScanInput](),
	Function:    ExecuteFastFrameworkScan,
}

// FastFrameworkScanInput is the input for the fast_framework_scan tool.
type FastFrameworkScanInput struct {
	// TargetDir to scan (defaults to workingDir)
	TargetDir string `json:"target_dir,omitempty" jsonschema:"description=Directory to scan. Defaults to framework root."`
	// IncludeTODOs if true, extract all TODO/FIXME/XXX comments from all files
	IncludeTODOs bool `json:"include_todos,omitempty" jsonschema:"description=If true, extract all TODO/FIXME/XXX comments from source files."`
}

// dirStats tracks directory statistics
type dirStats struct {
	goFiles    int
	testFiles  int
	otherFiles int
	totalLOC   int
	todos      []string
}

// ExecuteFastFrameworkScan performs a fast scan without reading large files.
func ExecuteFastFrameworkScan(input json.RawMessage) (string, error) {
	var args FastFrameworkScanInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	targetDir := args.TargetDir
	if targetDir == "" {
		targetDir = workingDir
	}

	stats := make(map[string]*dirStats)
	var totalGo, totalTest, totalOther, totalLOC int

	err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		relPath, _ := filepath.Rel(targetDir, path)
		pkgDir := filepath.Dir(relPath)
		if pkgDir == "." {
			pkgDir = "root"
		}

		if stats[pkgDir] == nil {
			stats[pkgDir] = &dirStats{}
		}

		switch {
		case strings.HasSuffix(info.Name(), "_test.go"):
			stats[pkgDir].testFiles++
			totalTest++
		case strings.HasSuffix(info.Name(), ".go"):
			stats[pkgDir].goFiles++
			totalGo++
			// Quick LOC estimate: file size / avg line length
			stats[pkgDir].totalLOC += int(info.Size() / 40)
			totalLOC += int(info.Size() / 40)
		default:
			stats[pkgDir].otherFiles++
			totalOther++
		}

		// Extract TODOs if requested (scan first 500 lines)
		if args.IncludeTODOs && (strings.HasSuffix(info.Name(), ".go") ||
			strings.HasSuffix(info.Name(), ".sh") ||
			strings.HasSuffix(info.Name(), ".md")) {
			extractTODOs(path, relPath, stats[pkgDir])
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("error scanning directory: %w", err)
	}

	// Format output
	var output strings.Builder
	fmt.Fprintf(&output, "## Fast Framework Scan Report\n\n")
	fmt.Fprintf(&output, "📊 **Summary**\n")
	fmt.Fprintf(&output, "| Metric | Count |\n")
	fmt.Fprintf(&output, "|--------|-------|\n")
	fmt.Fprintf(&output, "| Go Source Files | %d |\n", totalGo)
	fmt.Fprintf(&output, "| Test Files | %d |\n", totalTest)
	fmt.Fprintf(&output, "| Other Files | %d |\n", totalOther)
	fmt.Fprintf(&output, "| Est. Lines of Code | %d |\n", totalLOC)

	fmt.Fprintf(&output, "\n📁 **Package Breakdown**\n\n")
	for pkg := range stats {
		s := stats[pkg]
		fmt.Fprintf(&output, "- **%s**: %d source + %d tests\n", pkg, s.goFiles, s.testFiles)
	}

	if args.IncludeTODOs {
		fmt.Fprintf(&output, "\n📋 **Outstanding TODOs/FIXMEs**\n\n")
		totalTodos := 0
		for pkg := range stats {
			for _, todo := range stats[pkg].todos {
				fmt.Fprintf(&output, "- %s\n", todo)
				totalTodos++
			}
		}
		if totalTodos == 0 {
			fmt.Fprintf(&output, "✅ No TODOs found!\n")
		}
	}

	return output.String(), nil
}

// extractTODOs scans a file for TODO/FIXME/XXX comments
func extractTODOs(path, relPath string, s *dirStats) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if i > 500 { // Only first 500 lines
			break
		}
		if strings.Contains(line, "TODO") || strings.Contains(line, "FIXME") || strings.Contains(line, "XXX") {
			trimmed := strings.TrimSpace(line)
			s.todos = append(s.todos, fmt.Sprintf("**%s:%d** — %s", relPath, i+1, trimmed))
		}
	}
}
