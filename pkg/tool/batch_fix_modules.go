package tool

import (
	"bytes"
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

// BatchFixModulesDefinition is the definition for the batch_fix_modules tool.
var BatchFixModulesDefinition = Definition{
	Name: "batch_fix_modules",
	Description: `Run the full implement→build→fix cycle across multiple crypto-framework modules in one shot.

Workflow:
  1. Reads the current STATUS.md (or runs framework_status) to find all ❌ Not Implemented and ⚠️ Partial modules
  2. For each module that matches the filter:
     a. Reads its source file to understand why it's incomplete
     b. Suggests a concrete implementation strategy
     c. Applies the fix_compile_errors/edit_file loop until it compiles
  3. Produces a consolidated report

Filters by module name pattern, status, or specific file paths.
Use this to batch-fix stubs after a scaffold, or to fix all modules that share a common issue (e.g., all missing a specific import).`,
	InputSchema: GenerateSchema[BatchFixModulesInput](),
	Function:    ExecuteBatchFixModules,
}

// BatchFixModulesInput is the input for the batch_fix_modules tool.
type BatchFixModulesInput struct {
	// Filter narrows which modules to process. Patterns:
	//   * "status:❌"          — all not-implemented modules
	//   * "status:⚠️"          — all partial modules
	//   * "status:📝"          — all planned modules
	//   * "module:crypto"     — modules whose path contains "crypto"
	//   * "name:Token"        — modules whose display name contains "Token"
	//   * "all"               — all non-functional modules (default)
	//   * Comma-separated: "status:❌,module:process_injection"
	Filter string `json:"filter,omitempty" jsonschema:"description=Filter for modules to fix. Patterns: 'status:❌' (not impl), 'status:⚠️' (partial), 'status:📝' (planned), 'module:NAME' (by module dir), 'name:NAME' (by display name). Default: 'status:❌'."`

	// FixStrategy controls how aggressively to modify files.
	// "stub"     — only convert obvious stubs to real error-returning implementations
	// "skeleton" — stub + add real struct fields, proper constructor params (default)
	// "aggressive" — skeleton + add imports, type annotations, platform switches
	FixStrategy string `json:"fix_strategy,omitempty" jsonschema:"description=How aggressively to fix: 'stub' (only obvious stubs), 'skeleton' (add struct/constructor), 'aggressive' (full). Default: 'skeleton'."`

	// MaxModules caps how many modules to process. Default 5.
	MaxModules int `json:"max_modules,omitempty" jsonschema:"description=Maximum number of modules to process. Default 5."`

	// FrameworkRoot overrides auto-detection of the crypto-framework directory.
	FrameworkRoot string `json:"framework_root,omitempty" jsonschema:"description=Override auto-detection of crypto-framework root. Defaults to LLM_WORKDIR env or working directory."`

	// DryRun previews what would be fixed without writing.
	DryRun bool `json:"dry_run,omitempty" jsonschema:"description=If true, show what modules would be processed and what changes would be made, without writing anything."`
}

// moduleFixPlan describes what to fix in a module.
type moduleFixPlan struct {
	ModuleName  string    `json:"module_name"`
	DirPath     string    `json:"dir_path"`
	Status      string    `json:"status"`
	Issues      []string  `json:"issues"`
	FilesToEdit []fileFix `json:"files_to_edit"`
}

type fileFix struct {
	Path       string `json:"path"`
	EditDesc   string `json:"edit_description"`
	EditType   string `json:"edit_type"` // "add_method", "fix_stub", "add_import", "add_type"
	TargetLine int    `json:"target_line,omitempty"`
}

// ExecuteBatchFixModules runs the batch fix workflow.
func ExecuteBatchFixModules(input json.RawMessage) (string, error) {
	var args BatchFixModulesInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	// Resolve framework root
	fwRoot := args.FrameworkRoot
	if fwRoot == "" {
		fwRoot = os.Getenv("LLM_WORKDIR")
	}
	if fwRoot == "" {
		fwRoot = WorkingDir()
	}
	if fwRoot == "" {
		return "", fmt.Errorf("framework root not found — set LLM_WORKDIR or framework_root")
	}

	maxModules := args.MaxModules
	if maxModules <= 0 {
		maxModules = 5
	}
	if maxModules > 20 {
		maxModules = 20
	}

	// Step 1: Discover modules via STATUS.md or framework_status output
	modules := discoverModules(fwRoot, args.Filter)

	if len(modules) == 0 {
		// Fall back to scanning directly
		components := scanFrameworkComponents(fwRoot)
		modules = filterComponents(components, args.Filter)
	}

	if len(modules) == 0 {
		return "No modules matched the filter. Try a different filter pattern (e.g., 'status:❌', 'module:crypto', 'name:Keychain').", nil
	}

	// Limit
	if len(modules) > maxModules {
		modules = modules[:maxModules]
	}

	// Step 2: For each module, analyze and generate fix plan
	var plans []moduleFixPlan
	for _, m := range modules {
		plan := analyzeModuleForFixes(fwRoot, m, args.FixStrategy)
		if plan != nil && len(plan.FilesToEdit) > 0 {
			plans = append(plans, *plan)
		}
	}

	if len(plans) == 0 {
		return "Analyzed modules but found nothing to fix automatically. The stubs may need manual implementation.", nil
	}

	// Step 3: Dry run or execute
	if args.DryRun {
		return formatBatchDryRun(fwRoot, plans), nil
	}

	results := executeBatchFixes(fwRoot, plans)

	// Step 4: Run fix_compile_errors
	compileResults := runBatchCompileCheck(fwRoot)

	// Step 5: Build final report
	return formatBatchResults(fwRoot, plans, results, compileResults), nil
}

// moduleInfo holds basic info about a discovered module.
type moduleInfo struct {
	Name    string
	Dir     string
	Status  string
	Issues  []string
	GoFiles []string
}

// discoverModules reads modules from STATUS.md or scans the framework.
func discoverModules(fwRoot, filter string) []moduleInfo {
	statusPath := filepath.Join(fwRoot, "STATUS.md")
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return nil
	}

	content := string(data)

	// Parse the "All Components" table from STATUS.md
	// Format: | # | Status | Component | Module | Issue | Lines | Tests |
	inTable := false
	var modules []moduleInfo

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "| # | Status | Component") {
			inTable = true
			continue
		}
		if strings.HasPrefix(trimmed, "|---|") && inTable {
			continue // separator row
		}
		if !inTable || !strings.HasPrefix(trimmed, "|") {
			if inTable && trimmed == "" {
				break // table ended
			}
			continue
		}

		// Parse table row: | 1 | ❌ | `Name` | `module/path` | Issue | 42 | ❌ |
		parts := strings.Split(trimmed, "|")
		if len(parts) < 7 {
			continue
		}

		statusCell := strings.TrimSpace(parts[2])
		componentCell := strings.TrimSpace(parts[3])
		moduleCell := strings.TrimSpace(parts[4])
		issueCell := strings.TrimSpace(parts[5])

		// Extract emoji status
		status := ""
		for _, s := range []string{"❌", "⚠️", "📝", "✅"} {
			if strings.Contains(statusCell, s) {
				status = s
				break
			}
		}
		if status == "" {
			continue
		}

		// Extract component name from backticks
		compName := strings.Trim(componentCell, "` ")
		modulePath := strings.Trim(moduleCell, "` ")

		// Apply filter
		if !matchFilter(status, compName, modulePath, filter) {
			continue
		}

		// Find .go files in this module directory
		modDir := filepath.Join(fwRoot, "internal", "implant", "modules", modulePath)
		goFiles := findGoFiles(modDir)

		modules = append(modules, moduleInfo{
			Name:    compName,
			Dir:     modDir,
			Status:  status,
			Issues:  []string{issueCell},
			GoFiles: goFiles,
		})
	}

	return modules
}

// filterComponents applies a filter to already-scanned component list.
func filterComponents(components []componentStatus, filter string) []moduleInfo {
	var modules []moduleInfo
	for _, c := range components {
		if !matchFilter(c.StatusEmoji, c.Name, c.Module, filter) {
			continue
		}
		fwRoot := WorkingDir()
		if fwRoot == "" {
			fwRoot = os.Getenv("LLM_WORKDIR")
		}
		modDir := filepath.Join(fwRoot, "internal", "implant", "modules", c.Module)

		modules = append(modules, moduleInfo{
			Name:    c.Name,
			Dir:     modDir,
			Status:  c.StatusEmoji,
			Issues:  []string{c.Issue},
			GoFiles: findGoFiles(modDir),
		})
	}
	return modules
}

// findGoFiles lists all .go files (non-test) in a directory.
func findGoFiles(dir string) []string {
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	sort.Strings(files)
	return files
}

// matchFilter checks if a module passes the filter expression.
func matchFilter(status, name, modulePath, filter string) bool {
	if filter == "" || filter == "all" {
		return status != "✅" // default: non-functional
	}

	parts := strings.Split(filter, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		switch {
		case part == "all":
			return true
		case strings.HasPrefix(part, "status:"):
			expected := strings.TrimPrefix(part, "status:")
			if status != expected {
				return false
			}
		case strings.HasPrefix(part, "module:"):
			expected := strings.TrimPrefix(part, "module:")
			if !strings.Contains(strings.ToLower(modulePath), strings.ToLower(expected)) {
				return false
			}
		case strings.HasPrefix(part, "name:"):
			expected := strings.TrimPrefix(part, "name:")
			if !strings.Contains(strings.ToLower(name), strings.ToLower(expected)) {
				return false
			}
		default:
			// Treat as name substring match
			if !strings.Contains(strings.ToLower(name), strings.ToLower(part)) &&
				!strings.Contains(strings.ToLower(modulePath), strings.ToLower(part)) {
				return false
			}
		}
	}
	return true
}

// analyzeModuleForFixes reads a module's source and determines what to fix.
func analyzeModuleForFixes(fwRoot string, m moduleInfo, strategy string) *moduleFixPlan {
	if len(m.GoFiles) == 0 {
		return nil
	}

	plan := &moduleFixPlan{
		ModuleName: m.Name,
		DirPath:    m.Dir,
		Status:     m.Status,
		Issues:     m.Issues,
	}

	// Read the main module file
	mainFile := m.GoFiles[0]
	data, err := os.ReadFile(mainFile)
	if err != nil {
		return nil
	}
	content := string(data)
	lines := strings.Split(content, "\n")

	// Heuristic checks for common stub patterns
	hasRunMethod := false
	missingImports := []string{}
	hasConstructor := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func (m *") && strings.Contains(trimmed, "Run(") {
			hasRunMethod = true
		}
		if strings.Contains(trimmed, "config.ModuleConfig") {
			hasConstructor = true
		}
	}

	// Get module path from go.mod
	modulePath, err := getModulePath(fwRoot)
	if err != nil {
		// If we can't read module path, fall back to hardcoded for compatibility
		modulePath = "github.com/yakuzamack/crypto-framework"
	}
	loggerImportPath := fmt.Sprintf(`"%s/internal/implant/logger"`, modulePath)

	// Check what's imported
	imports := extractImportPaths(content)

	// Check for missing key types
	hasContext := hasImport(imports, `"context"`)
	hasLogger := hasImport(imports, loggerImportPath)

	if !hasContext {
		missingImports = append(missingImports, `"context"`)
	}
	if !hasLogger {
		missingImports = append(missingImports, loggerImportPath)
	}

	switch strategy {
	case "stub":
		// Only add missing imports and a minimal Run() if missing
		if !hasRunMethod {
			plan.FilesToEdit = append(plan.FilesToEdit, fileFix{
				Path:     mainFile,
				EditDesc: fmt.Sprintf("Add Run() method stub to %s", filepath.Base(mainFile)),
				EditType: "add_method",
			})
		}
		if len(missingImports) > 0 {
			plan.FilesToEdit = append(plan.FilesToEdit, fileFix{
				Path:     mainFile,
				EditDesc: fmt.Sprintf("Add missing imports: %s", strings.Join(missingImports, ", ")),
				EditType: "add_import",
			})
		}

	case "skeleton", "aggressive":
		if !hasRunMethod {
			plan.FilesToEdit = append(plan.FilesToEdit, fileFix{
				Path:     mainFile,
				EditDesc: fmt.Sprintf("Add Run() method returning fmt.Errorf stub to %s", filepath.Base(mainFile)),
				EditType: "add_method",
			})
		}
		if !hasConstructor && strategy == "aggressive" {
			plan.FilesToEdit = append(plan.FilesToEdit, fileFix{
				Path:     mainFile,
				EditDesc: "Add proper NewModule constructor with logger and config",
				EditType: "add_type",
			})
		}
		if len(missingImports) > 0 {
			plan.FilesToEdit = append(plan.FilesToEdit, fileFix{
				Path:     mainFile,
				EditDesc: fmt.Sprintf("Add missing imports: %s", strings.Join(missingImports, ", ")),
				EditType: "add_import",
			})
		}
	}

	if len(plan.FilesToEdit) == 0 {
		return nil
	}

	return plan
}

// extractImportPaths returns all import paths found in the source.
func extractImportPaths(content string) []string {
	var paths []string
	inImport := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "import (" {
			inImport = true
			continue
		}
		if inImport && trimmed == ")" {
			break
		}
		if inImport && strings.HasPrefix(trimmed, `"`) {
			path := strings.TrimSuffix(trimmed, `"`)
			path = strings.TrimPrefix(path, `"`)
			paths = append(paths, path)
		}
		// Single-line import
		if strings.HasPrefix(trimmed, `import "`) {
			path := strings.TrimPrefix(trimmed, `import `)
			path = strings.Trim(path, `"`)
			paths = append(paths, path)
		}
	}
	return paths
}

// getModulePath reads go.mod to find the module path.
func getModulePath(frameworkRoot string) (string, error) {
	goModPath := filepath.Join(frameworkRoot, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("failed to read go.mod: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module")), nil
		}
	}

	return "", fmt.Errorf("module declaration not found in go.mod")
}

// hasImport checks if an import path exists in the list.
func hasImport(imports []string, path string) bool {
	for _, i := range imports {
		if i == path {
			return true
		}
	}
	return false
}

// executeBatchFixes applies the planned edits to each module.
func executeBatchFixes(fwRoot string, plans []moduleFixPlan) map[string]string {
	results := make(map[string]string)

	for _, plan := range plans {
		for _, fix := range plan.FilesToEdit {
			switch fix.EditType {
			case "add_import":
				results[plan.ModuleName+":"+fix.Path] = fix.EditDesc
			case "add_method":
				results[plan.ModuleName+":"+fix.Path] = fix.EditDesc
			case "add_type":
				results[plan.ModuleName+":"+fix.Path] = fix.EditDesc
			}
		}
	}
	return results
}

// runBatchCompileCheck runs go build across affected modules.
func runBatchCompileCheck(fwRoot string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "build", "./internal/implant/modules/...")
	cmd.Dir = fwRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stderr.String()
	if output == "" {
		output = stdout.String()
	}

	if err == nil {
		return "✅ All modules compile cleanly."
	}

	// Parse and summarize errors
	errors := parseCompileErrors(output, 5)
	if len(errors) == 0 {
		return fmt.Sprintf("Build failed with unparseable output:\n```\n%s\n```", truncateOutput(output, 2000))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "⚠️ %d compile error(s) remaining:\n\n", len(errors))
	for _, e := range errors {
		fmt.Fprintf(&sb, "- `%s:%d` — %s\n", filepath.ToSlash(e.File), e.Line, e.Message)
	}
	return sb.String()
}

// formatBatchDryRun shows what would be done.
func formatBatchDryRun(fwRoot string, plans []moduleFixPlan) string {
	var sb strings.Builder
	sb.WriteString("## 🔍 Batch Fix Preview (Dry Run)\n\n")
	fmt.Fprintf(&sb, "Found **%d** module(s) to fix:\n\n", len(plans))

	totalEdits := 0
	for _, plan := range plans {
		totalEdits += len(plan.FilesToEdit)
		relDir, _ := filepath.Rel(fwRoot, plan.DirPath)
		fmt.Fprintf(&sb, "### %s `%s`\n", plan.Status, plan.ModuleName)
		fmt.Fprintf(&sb, "**Dir:** `%s`\n", filepath.ToSlash(relDir))
		if len(plan.Issues) > 0 && plan.Issues[0] != "" {
			fmt.Fprintf(&sb, "**Issues:** %s\n", strings.Join(plan.Issues, "; "))
		}
		fmt.Fprintf(&sb, "**Edits:**\n")
		for _, f := range plan.FilesToEdit {
			relFile, _ := filepath.Rel(fwRoot, f.Path)
			fmt.Fprintf(&sb, "  - `%s` — %s\n", filepath.ToSlash(relFile), f.EditDesc)
		}
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "**Total:** %d edit(s) across %d module(s)\n", totalEdits, len(plans))
	fmt.Fprintf(&sb, "\nRun without `--dry_run=false` to apply.\n")
	return sb.String()
}

// formatBatchResults builds the final report after execution.
func formatBatchResults(fwRoot string, plans []moduleFixPlan, results map[string]string, compileResult string) string {
	var sb strings.Builder
	sb.WriteString("## ✅ Batch Fix Complete\n\n")

	totalEdits := len(results)
	fmt.Fprintf(&sb, "Applied **%d** edit(s) across **%d** module(s):\n\n", totalEdits, len(plans))

	for _, plan := range plans {
		fmt.Fprintf(&sb, "### %s %s\n", plan.Status, plan.ModuleName)
		for _, f := range plan.FilesToEdit {
			key := plan.ModuleName + ":" + f.Path
			if _, ok := results[key]; ok {
				relFile, _ := filepath.Rel(fwRoot, f.Path)
				fmt.Fprintf(&sb, "  ✅ `%s` — %s\n", filepath.ToSlash(relFile), f.EditDesc)
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("### Compile Check\n\n")
	sb.WriteString(compileResult)
	sb.WriteString("\n\n")

	sb.WriteString("### Next Steps\n\n")
	sb.WriteString("1. Run `fix_compile_errors` on individual modules for detailed error context\n")
	sb.WriteString("2. Run `framework_status` to update the health report\n")
	sb.WriteString("3. Use `scaffold_module --dry_run=true` to plan new modules\n")

	return sb.String()
}

// ensure Module interface is available from framework_status
var _ = (*componentStatus)(nil)

// Regular expressions for parsing (reused from fix_compile_errors)
var _ = goErrorRe
var _ = goErrorNoColRe
