package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FrameworkStatusDefinition is the definition for the framework_status tool.
var FrameworkStatusDefinition = Definition{
	Name:        "framework_status",
	Description: "Analyze the crypto-framework and produce a structured STATUS.md report. Scans all source files to detect stubs, placeholders, TODOs, and real implementations. Tracks what was planned vs. what was actually built. Provides a single source of truth for project health.",
	InputSchema: GenerateSchema[FrameworkStatusInput](),
	Function:    ExecuteFrameworkStatus,
}

// FrameworkStatusInput is the input for the framework_status tool.
type FrameworkStatusInput struct {
	// OutputPath is where to write STATUS.md. Defaults to <framework-root>/STATUS.md.
	OutputPath string `json:"output_path,omitempty" jsonschema:"description=Path to write STATUS.md. Defaults to framework-root/STATUS.md."`
	// Regenerate forces a full re-scan even if cached data exists.
	Regenerate bool `json:"regenerate,omitempty" jsonschema:"description=If true, force a full re-scan and overwrite any existing STATUS.md."`
}

// componentStatus tracks the state of a framework component.
type componentStatus struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Status      string `json:"status"`       // ✅ Functional | ❌ Not Implemented | ⚠️ Partial | 📝 Planned
	StatusEmoji string `json:"status_emoji"` // For display
	Issue       string `json:"issue"`        // What's wrong (if not ✅)
	SourceLines int    `json:"source_lines"`
	HasTests    bool   `json:"has_tests"`
	Module      string `json:"module"` // e.g., "crypto/extraction", "process_injection"
}

// ExecuteFrameworkStatus scans the framework and produces STATUS.md.
func ExecuteFrameworkStatus(input json.RawMessage) (string, error) {
	var args FrameworkStatusInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	fwDir := workingDir
	if fwDir == "" {
		fwDir = os.Getenv("LLM_WORKDIR")
	}
	if fwDir == "" {
		return "No framework directory configured. Set LLM_WORKDIR or run from the framework directory.", nil
	}

	info, err := os.Stat(fwDir)
	if err != nil {
		return "", fmt.Errorf("cannot access framework directory %q: %w", fwDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", fwDir)
	}

	outputPath := args.OutputPath
	if outputPath == "" {
		outputPath = filepath.Join(fwDir, "STATUS.md")
	}

	// Step 1: Scan all Go source files
	components := scanFrameworkComponents(fwDir)

	// Step 2: Check for tests
	enrichWithTestInfo(fwDir, components)

	// Step 3: Generate the report
	report := generateStatusReport(fwDir, components)

	// Step 4: Write STATUS.md
	err = os.WriteFile(outputPath, []byte(report), 0644)
	if err != nil {
		return "", fmt.Errorf("failed to write STATUS.md: %w", err)
	}

	// Step 5: Return summary to the agent
	var functional, partial, notImpl, planned int
	for _, c := range components {
		switch c.Status {
		case "✅ Functional":
			functional++
		case "⚠️ Partial":
			partial++
		case "❌ Not Implemented":
			notImpl++
		case "📝 Planned":
			planned++
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Framework Status Report\n\n")
	fmt.Fprintf(&sb, "**Written to:** `%s`\n\n", outputPath)
	fmt.Fprintf(&sb, "### Summary\n\n")
	fmt.Fprintf(&sb, "| Status | Count |\n")
	fmt.Fprintf(&sb, "|--------|-------|\n")
	fmt.Fprintf(&sb, "| ✅ Functional | %d |\n", functional)
	fmt.Fprintf(&sb, "| ⚠️ Partial | %d |\n", partial)
	fmt.Fprintf(&sb, "| ❌ Not Implemented (stub/placeholder) | %d |\n", notImpl)
	fmt.Fprintf(&sb, "| 📝 Planned (documented only) | %d |\n", planned)
	fmt.Fprintf(&sb, "| **Total** | **%d** |\n", len(components))

	if notImpl > 0 {
		fmt.Fprintf(&sb, "\n### ❌ Items That Need Work\n\n")
		for _, c := range components {
			if c.Status == "❌ Not Implemented" {
				fmt.Fprintf(&sb, "- **%s** (`%s`) — %s\n", c.Name, c.Path, c.Issue)
			}
		}
	}

	fmt.Fprintf(&sb, "\n> Run `pinecone_ingest --reset_index=true` to re-index this status into the knowledge base.\n")

	return sb.String(), nil
}

// scanFrameworkComponents walks the implant modules directory and analyzes each component.
func scanFrameworkComponents(fwDir string) []componentStatus {
	// Focus on the implant modules — the core of the framework
	modulesDir := filepath.Join(fwDir, "internal", "implant", "modules")

	var components []componentStatus

	// Walk all .go files in modules
	if err := filepath.Walk(modulesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}
		// Skip test files (they're analyzed separately)
		if strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}

		c := analyzeGoFile(path, fwDir)
		if c != nil {
			components = append(components, *c)
		}
		return nil
	}); err != nil {
		// Log error but continue with what we have
		fmt.Printf("Warning: error walking modules directory: %v\n", err)
	}

	// Sort: not implemented first, then partial, then planned, then functional
	sort.Slice(components, func(i, j int) bool {
		order := map[string]int{
			"❌ Not Implemented": 0,
			"⚠️ Partial":        1,
			"📝 Planned":         2,
			"✅ Functional":      3,
		}
		oi := order[components[i].Status]
		oj := order[components[j].Status]
		if oi != oj {
			return oi < oj
		}
		return components[i].Name < components[j].Name
	})

	return components
}

// analyzeGoFile reads a Go source file and determines its implementation status.
func analyzeGoFile(path, fwDir string) *componentStatus {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(data)
	lines := strings.Split(content, "\n")
	relPath, _ := filepath.Rel(fwDir, path)
	relPath = filepath.ToSlash(relPath)

	name := extractComponentName(path)
	module := extractModuleName(path, fwDir)

	// Check for stub/placeholder indicators
	placeholderPatterns := []string{
		`Note: Real implementation would`,
		`Note: Real attack would`,
		`Note: Exodus stores`,
		`Injection prepared`,
		`Remote thread creation ready`,
		`not implemented`,
		`"Note:`,
	}
	printOnlyIndicators := []string{
		`WriteString("✓`,
		`fmt.Printf("✓`,
		`fmt.Println("✓`,
	}

	// Check for stub pattern: package name suggests stub, or returns errors saying "not implemented"
	isStub := false
	hasTodo := false
	issueReasons := []string{}

	// Check file name for "_stub" or "_fallback" or "_other"
	baseName := strings.ToLower(filepath.Base(path))
	if strings.Contains(baseName, "_stub") || strings.Contains(baseName, "_fallback") || strings.Contains(baseName, "_other") {
		isStub = true
		issueReasons = append(issueReasons, "filename indicates stub/fallback variant")
	}

	// Check for stub comment at top of file
	if len(lines) > 0 && strings.Contains(strings.ToLower(lines[0]), "stub") {
		isStub = true
		issueReasons = append(issueReasons, "file header says stub")
	}

	// Check content for placeholder indicators
	for _, pattern := range placeholderPatterns {
		if strings.Contains(content, pattern) {
			isStub = true
			issueReasons = append(issueReasons, fmt.Sprintf("contains placeholder text: %q", truncate(pattern, 40)))
		}
	}

	// Check for print-only (fake success messages without real logic)
	for _, pattern := range printOnlyIndicators {
		if strings.Contains(content, pattern) {
			isStub = true
			issueReasons = append(issueReasons, "prints fake success messages without real implementation")
		}
	}

	// Check for TODO/FIXME
	if strings.Contains(content, "TODO") || strings.Contains(content, "FIXME") {
		hasTodo = true
	}

	// Check if function bodies are empty or just return errors
	emptyFuncCount := countPrintOnlyOrErrorOnlyFunctions(content)
	if emptyFuncCount > 2 && !isStub {
		isStub = true
		issueReasons = append(issueReasons, fmt.Sprintf("%d functions return only errors or print text", emptyFuncCount))
	}

	// Check for "return nil, fmt.Errorf" patterns that are the only logic
	errorReturnCount := strings.Count(content, "return nil, fmt.Errorf")
	if errorReturnCount > 3 && !isStub {
		isStub = true
		issueReasons = append(issueReasons, fmt.Sprintf("%d functions just return errors", errorReturnCount))
	}

	// Determine status
	status := "✅ Functional"
	statusEmoji := "✅"
	issue := ""

	if isStub {
		status = "❌ Not Implemented"
		statusEmoji = "❌"
		issue = strings.Join(issueReasons, "; ")
	} else if hasTodo {
		status = "⚠️ Partial"
		statusEmoji = "⚠️"
		issue = "contains TODO/FIXME markers"
	} else if len(lines) < 10 {
		status = "📝 Planned"
		statusEmoji = "📝"
		issue = "minimal file, likely early stage"
	}

	// Skip files that are clearly utility/config (not components)
	if strings.Contains(relPath, "types") || strings.Contains(relPath, "config") {
		return nil
	}

	return &componentStatus{
		Name:        name,
		Path:        relPath,
		Status:      status,
		StatusEmoji: statusEmoji,
		Issue:       issue,
		SourceLines: len(lines),
		HasTests:    false, // filled in later
		Module:      module,
	}
}

// extractComponentName creates a human-readable name from a file path.
func extractComponentName(path string) string {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, ".go")
	// Clean up common prefixes/suffixes
	name = strings.TrimPrefix(name, "cmd_")
	name = strings.TrimPrefix(name, "crypto_")
	name = strings.TrimSuffix(name, "_stub")
	name = strings.TrimSuffix(name, "_fallback")
	name = strings.TrimSuffix(name, "_other")
	// Convert snake_case to Title Case
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// extractModuleName extracts the module path relative to the modules dir.
func extractModuleName(path, fwDir string) string {
	modulesDir := filepath.Join(fwDir, "internal", "implant", "modules")
	rel, err := filepath.Rel(modulesDir, path)
	if err != nil {
		return ""
	}
	// Return just the directory part (e.g., "crypto/extraction")
	dir := filepath.Dir(rel)
	return filepath.ToSlash(dir)
}

// countPrintOnlyOrErrorOnlyFunctions counts functions that only print or return errors.
func countPrintOnlyOrErrorOnlyFunctions(content string) int {
	count := 0
	lines := strings.Split(content, "\n")
	inFunc := false
	depth := 0
	hasRealLogic := false
	openingBraceFound := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		if strings.HasPrefix(trimmed, "func ") {
			// Finalize previous function
			if inFunc && openingBraceFound && !hasRealLogic {
				count++
			}
			// Start new function
			inFunc = true
			depth = 0
			hasRealLogic = false
			openingBraceFound = false

			// Check if opening brace is on the same line
			if strings.Contains(trimmed, "{") {
				openingBraceFound = true
				depth += strings.Count(trimmed, "{")
				depth -= strings.Count(trimmed, "}")
			}
			continue
		}

		if inFunc {
			depth += strings.Count(trimmed, "{")
			depth -= strings.Count(trimmed, "}")

			if strings.Contains(trimmed, "{") {
				openingBraceFound = true
			}

			if !hasRealLogic && openingBraceFound {
				// These alone don't count as "real logic"
				isNoise := strings.Contains(trimmed, "WriteString") ||
					strings.Contains(trimmed, "fmt.Print") ||
					strings.Contains(trimmed, "return ") ||
					strings.HasPrefix(trimmed, "//")

				if !isNoise {
					if strings.Contains(trimmed, "=") ||
						strings.Contains(trimmed, "if ") ||
						strings.Contains(trimmed, "for ") ||
						strings.Contains(trimmed, "switch ") ||
						strings.Contains(trimmed, "go ") ||
						strings.Contains(trimmed, "make(") ||
						strings.Contains(trimmed, "append(") ||
						strings.Contains(trimmed, "new(") ||
						strings.Contains(trimmed, "defer ") ||
						strings.Contains(trimmed, "select ") {
						hasRealLogic = true
					}
				}
			}

			if depth <= 0 && openingBraceFound {
				// End of function
				if !hasRealLogic {
					count++
				}
				inFunc = false
			}
		}
	}

	// Handle case where file ends while still in a function
	if inFunc && openingBraceFound && !hasRealLogic {
		count++
	}

	return count
}

// enrichWithTestInfo checks if each component has a corresponding test file.
func enrichWithTestInfo(fwDir string, components []componentStatus) {
	for i, c := range components {
		testPath := strings.TrimSuffix(c.Path, ".go") + "_test.go"
		fullTestPath := filepath.Join(fwDir, testPath)
		if _, err := os.Stat(fullTestPath); err == nil {
			components[i].HasTests = true
		}
	}
}

// generateStatusReport produces the full markdown report.
func generateStatusReport(fwDir string, components []componentStatus) string {
	var sb strings.Builder

	sb.WriteString("# Framework Status\n\n")
	fmt.Fprintf(&sb, "> **Auto-generated:** %s\n", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&sb, "> **Framework root:** `%s`\n", fwDir)
	sb.WriteString("> **Source:** `framework_status` tool\n\n")
	sb.WriteString("This file is automatically generated. Run `framework_status` to refresh it.\n\n")

	// Summary table
	sb.WriteString("## Summary\n\n")
	sb.WriteString("| Status | Count | Meaning |\n")
	sb.WriteString("|--------|-------|--------|\n")

	var functional, partial, notImpl, planned, totalTested int
	for _, c := range components {
		switch c.Status {
		case "✅ Functional":
			functional++
		case "⚠️ Partial":
			partial++
		case "❌ Not Implemented":
			notImpl++
		case "📝 Planned":
			planned++
		}
		if c.HasTests {
			totalTested++
		}
	}

	fmt.Fprintf(&sb, "| ✅ Functional | %d | Real implementation, working |\n", functional)
	fmt.Fprintf(&sb, "| ⚠️ Partial | %d | Has code but contains TODOs or incomplete |\n", partial)
	fmt.Fprintf(&sb, "| ❌ Not Implemented | %d | Stub / placeholder / print-only |\n", notImpl)
	fmt.Fprintf(&sb, "| 📝 Planned | %d | Minimal file, early stage |\n", planned)
	fmt.Fprintf(&sb, "| **Total** | **%d** | |\n", len(components))
	fmt.Fprintf(&sb, "| ✅ Has tests | %d / %d | |\n\n", totalTested, len(components))

	// Health score
	healthScore := 0
	if len(components) > 0 {
		healthScore = (functional * 100) / len(components)
	}
	fmt.Fprintf(&sb, "**Health Score:** %d%% functional (%d/%d components)\n\n", healthScore, functional, len(components))

	// Section by module
	sb.WriteString("## Components by Module\n\n")

	// Group by module
	byModule := make(map[string][]componentStatus)
	for _, c := range components {
		byModule[c.Module] = append(byModule[c.Module], c)
	}

	// Sort module names
	var moduleNames []string
	for m := range byModule {
		moduleNames = append(moduleNames, m)
	}
	sort.Strings(moduleNames)

	for _, module := range moduleNames {
		comps := byModule[module]
			fmt.Fprintf(&sb, "### %s\n\n", module)
		sb.WriteString("| Component | Status | Issue | Lines | Tests |\n")
		sb.WriteString("|-----------|--------|-------|-------|-------|\n")

		for _, c := range comps {
			testMark := ""
			if c.HasTests {
				testMark = "✅"
			} else {
				testMark = "❌"
			}
			issue := c.Issue
			if issue == "" {
				issue = "—"
			}
				fmt.Fprintf(&sb, "| %s %s | %s | %s | %d | %s |\n",
				c.StatusEmoji, c.Name, c.Status, issue, c.SourceLines, testMark)
		}
		sb.WriteString("\n")
	}

	// Full component table (sorted by status)
	sb.WriteString("## All Components (Sorted by Status)\n\n")
	sb.WriteString("| # | Status | Component | Module | Issue | Lines | Tests |\n")
	sb.WriteString("|---|--------|-----------|--------|-------|-------|-------|\n")

	for i, c := range components {
		testMark := ""
		if c.HasTests {
			testMark = "✅"
		} else {
			testMark = "❌"
		}
		issue := c.Issue
		if issue == "" {
			issue = "—"
		}
		fmt.Fprintf(&sb, "| %d | %s | `%s` | `%s` | %s | %d | %s |\n",
			i+1, c.StatusEmoji, c.Name, c.Module, issue, c.SourceLines, testMark)
	}

	// Action items
	sb.WriteString("\n## Action Items\n\n")

	notImplComps := filterByStatus(components, "❌ Not Implemented")
	if len(notImplComps) > 0 {
		sb.WriteString("### Must Fix (Stubs/Placeholders)\n\n")
		for _, c := range notImplComps {
			fmt.Fprintf(&sb, "- [ ] **%s** (`%s`) — %s\n", c.Name, c.Path, c.Issue)
		}
		sb.WriteString("\n")
	}

	partialComps := filterByStatus(components, "⚠️ Partial")
	if len(partialComps) > 0 {
		sb.WriteString("### Should Improve (TODO/FIXME)\n\n")
		for _, c := range partialComps {
			fmt.Fprintf(&sb, "- [ ] **%s** (`%s`) — %s\n", c.Name, c.Path, c.Issue)
		}
		sb.WriteString("\n")
	}

	// Legend
	sb.WriteString("## Legend\n\n")
	sb.WriteString("| Emoji | Meaning |\n")
	sb.WriteString("|-------|--------|\n")
	sb.WriteString("| ✅ | Fully functional — real implementation |\n")
	sb.WriteString("| ⚠️ | Partial — has code but contains TODOs or incomplete sections |\n")
	sb.WriteString("| ❌ | Not implemented — stub, placeholder, or print-only |\n")
	sb.WriteString("| 📝 | Planned — minimal file, early development |\n")
	sb.WriteString("| 🟢 | Has unit tests |\n")
	sb.WriteString("| 🔴 | No unit tests |\n\n")

	return sb.String()
}

// filterByStatus returns components matching a given status string.
func filterByStatus(components []componentStatus, status string) []componentStatus {
	var result []componentStatus
	for _, c := range components {
		if c.Status == status {
			result = append(result, c)
		}
	}
	return result
}

// truncate shortens a string to maxLen.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
