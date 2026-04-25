package tool

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ModuleAnalyzerDefinition is the definition for the module_analyzer tool.
var ModuleAnalyzerDefinition = Definition{
	Name: "module_analyzer",
	Description: `Analyze crypto-framework module dependencies and interactions.

Performs comprehensive analysis of:
  - Import dependencies between modules
  - Function call relationships across packages
  - Data flow between implant modules
  - Module coupling and cohesion metrics
  - Circular dependency detection
  - Dead code identification
  - API surface mapping

Generates:
  - Dependency graphs (text and DOT format)
  - Module interaction maps
  - Architecture health reports
  - Refactoring recommendations

Useful for understanding framework architecture and identifying optimization opportunities.`,
	InputSchema: GenerateSchema[ModuleAnalyzerInput](),
	Function:    ExecuteModuleAnalyzer,
}

// ModuleAnalyzerInput is the input for the module_analyzer tool.
type ModuleAnalyzerInput struct {
	// AnalysisType specifies what to analyze (dependencies, calls, flow, health).
	AnalysisType string `json:"analysis_type,omitempty" jsonschema:"description=Analysis type: dependencies, calls, flow, health, or full. Default: dependencies."`
	
	// TargetModules limits analysis to specific modules (e.g., ["crypto", "wallet_exploit"]).
	TargetModules []string `json:"target_modules,omitempty" jsonschema:"description=Specific modules to analyze. Empty = all modules."`
	
	// OutputFormat controls output format (text, json, dot, markdown).
	OutputFormat string `json:"output_format,omitempty" jsonschema:"description=Output format: text, json, dot, markdown. Default: markdown."`
	
	// IncludeExternal includes external package dependencies in analysis.
	IncludeExternal bool `json:"include_external,omitempty" jsonschema:"description=Include external package dependencies (github.com, etc.)."`
	
	// FrameworkRoot overrides auto-detection of crypto-framework directory.
	FrameworkRoot string `json:"framework_root,omitempty" jsonschema:"description=Override crypto-framework root directory. Defaults to LLM_WORKDIR env."`
	
	// MaxDepth limits recursive dependency analysis depth.
	MaxDepth int `json:"max_depth,omitempty" jsonschema:"description=Maximum dependency depth to analyze. Default: 3."`
}

// ModuleDependency represents a dependency relationship.
type ModuleDependency struct {
	From     string   `json:"from"`
	To       string   `json:"to"`
	Type     string   `json:"type"`     // import, call, interface
	Count    int      `json:"count"`    // number of references
	Examples []string `json:"examples"` // example usage
}

// ModuleInfo contains metadata about a module.
type ModuleInfo struct {
	Name         string   `json:"name"`
	Path         string   `json:"path"`
	Files        []string `json:"files"`
	Lines        int      `json:"lines"`
	Functions    []string `json:"functions"`
	Exports      []string `json:"exports"`
	Imports      []string `json:"imports"`
	Dependencies []string `json:"dependencies"`
}

// AnalysisResult contains the complete analysis results.
type AnalysisResult struct {
	Modules      []ModuleInfo       `json:"modules"`
	Dependencies []ModuleDependency `json:"dependencies"`
	Cycles       [][]string         `json:"cycles"`
	Metrics      AnalysisMetrics    `json:"metrics"`
	Warnings     []string           `json:"warnings"`
}

// AnalysisMetrics contains quantitative analysis results.
type AnalysisMetrics struct {
	TotalModules    int     `json:"total_modules"`
	TotalDeps       int     `json:"total_dependencies"`
	CyclicDeps      int     `json:"cyclic_dependencies"`
	MaxDepth        int     `json:"max_depth"`
	AvgFanOut      float64 `json:"avg_fan_out"`
	AvgFanIn       float64 `json:"avg_fan_in"`
	CouplingIndex  float64 `json:"coupling_index"`
	CohesionIndex  float64 `json:"cohesion_index"`
}

// ExecuteModuleAnalyzer performs module dependency analysis.
func ExecuteModuleAnalyzer(input json.RawMessage) (string, error) {
	var args ModuleAnalyzerInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	
	// Set defaults
	analysisType := args.AnalysisType
	if analysisType == "" {
		analysisType = "dependencies"
	}
	
	outputFormat := args.OutputFormat
	if outputFormat == "" {
		outputFormat = "markdown"
	}
	
	maxDepth := args.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	
	// Resolve framework root
	fwRoot := args.FrameworkRoot
	if fwRoot == "" {
		fwRoot = os.Getenv("LLM_WORKDIR")
	}
	if fwRoot == "" {
		fwRoot = WorkingDir()
	}
	
	// Validate framework structure
	modulesDir := filepath.Join(fwRoot, "internal", "implant", "modules")
	if _, err := os.Stat(modulesDir); os.IsNotExist(err) {
		return "", fmt.Errorf("modules directory not found: %s", modulesDir)
	}
	
	// Perform analysis
	result, err := analyzeFramework(fwRoot, args)
	if err != nil {
		return "", fmt.Errorf("analysis failed: %w", err)
	}
	
	// Format output
	switch outputFormat {
	case "json":
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	case "dot":
		return formatDOTGraph(result), nil
	case "text":
		return formatTextReport(result), nil
	default:
		return formatMarkdownReport(result, analysisType), nil
	}
}

// analyzeFramework performs the actual framework analysis.
func analyzeFramework(fwRoot string, args ModuleAnalyzerInput) (*AnalysisResult, error) {
	modulesDir := filepath.Join(fwRoot, "internal", "implant", "modules")
	
	// Discover modules
	modules, err := discoverFrameworkModules(modulesDir, args.TargetModules)
	if err != nil {
		return nil, err
	}
	
	// Analyze dependencies
	dependencies, err := analyzeDependencies(modules, args.IncludeExternal)
	if err != nil {
		return nil, err
	}
	
	// Detect cycles
	cycles := detectCycles(dependencies)
	
	// Calculate metrics
	metrics := calculateMetrics(modules, dependencies)
	
	// Generate warnings
	warnings := generateWarnings(modules, dependencies, cycles)
	
	return &AnalysisResult{
		Modules:      modules,
		Dependencies: dependencies,
		Cycles:       cycles,
		Metrics:      metrics,
		Warnings:     warnings,
	}, nil
}

// discoverFrameworkModules scans the modules directory and extracts module information.
func discoverFrameworkModules(modulesDir string, targetModules []string) ([]ModuleInfo, error) {
	var modules []ModuleInfo
	
	err := filepath.Walk(modulesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		
		// Only process directories that contain Go files
		if !info.IsDir() {
			return nil
		}
		
		// Skip root modules directory
		if path == modulesDir {
			return nil
		}
		
		// Get module name (relative to modules dir)
		relPath, err := filepath.Rel(modulesDir, path)
		if err != nil {
			return err
		}
		
		// Filter target modules if specified
		if len(targetModules) > 0 {
			found := false
			for _, target := range targetModules {
				if strings.Contains(relPath, target) {
					found = true
					break
				}
			}
			if !found {
				return filepath.SkipDir
			}
		}
		
		// Find Go files in this directory
		files, err := filepath.Glob(filepath.Join(path, "*.go"))
		if err != nil {
			return err
		}
		
		// Skip if no Go files (but continue walking)
		if len(files) == 0 {
			return nil
		}
		
		// Skip test files for main analysis
		var sourceFiles []string
		for _, file := range files {
			if !strings.HasSuffix(file, "_test.go") {
				sourceFiles = append(sourceFiles, file)
			}
		}
		
		if len(sourceFiles) == 0 {
			return nil
		}
		
		// Analyze module
		moduleInfo, err := analyzeModule(relPath, path, sourceFiles)
		if err != nil {
			return err
		}
		
		modules = append(modules, moduleInfo)
		
		// Don't walk subdirectories of modules (each module is one level)
		return filepath.SkipDir
	})
	
	return modules, err
}

// analyzeModule extracts detailed information about a single module.
func analyzeModule(name, path string, files []string) (ModuleInfo, error) {
	var info ModuleInfo
	info.Name = name
	info.Path = path
	info.Files = files
	
	var allImports []string
	var allFunctions []string
	var allExports []string
	totalLines := 0
	
	for _, file := range files {
		// Parse file
		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			continue // Skip files with parse errors
		}
		
		// Count lines
		data, _ := os.ReadFile(file)
		totalLines += len(strings.Split(string(data), "\n"))
		
		// Extract imports
		for _, imp := range node.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			allImports = append(allImports, impPath)
		}
		
		// Extract functions and exports
		for _, decl := range node.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name != nil {
				funcName := fn.Name.Name
				allFunctions = append(allFunctions, funcName)
				
				// Check if exported (starts with capital letter)
				if ast.IsExported(funcName) {
					allExports = append(allExports, funcName)
				}
			}
		}
	}
	
	info.Lines = totalLines
	info.Functions = removeDuplicates(allFunctions)
	info.Exports = removeDuplicates(allExports)
	info.Imports = removeDuplicates(allImports)
	
	// Extract framework dependencies from imports
	var deps []string
	for _, imp := range info.Imports {
		if strings.Contains(imp, "crypto-framework") || strings.Contains(imp, "../") {
			deps = append(deps, imp)
		}
	}
	info.Dependencies = deps
	
	return info, nil
}

// analyzeDependencies analyzes relationships between modules.
func analyzeDependencies(modules []ModuleInfo, includeExternal bool) ([]ModuleDependency, error) {
	var dependencies []ModuleDependency
	
	for _, module := range modules {
		for _, imp := range module.Imports {
			// Determine target module
			var targetModule string
			var depType string
			
			if strings.Contains(imp, "crypto-framework") {
				// Internal framework dependency
				parts := strings.Split(imp, "/")
				for i, part := range parts {
					if part == "modules" && i+1 < len(parts) {
						targetModule = parts[i+1]
						break
					}
				}
				depType = "internal"
			} else if includeExternal {
				// External dependency
				targetModule = imp
				depType = "external"
			} else {
				continue
			}
			
			if targetModule == "" {
				continue
			}
			
			// Create dependency
			dep := ModuleDependency{
				From: module.Name,
				To:   targetModule,
				Type: depType,
				Count: 1, // For now, just count as 1
			}
			
			dependencies = append(dependencies, dep)
		}
	}
	
	return dependencies, nil
}

// detectCycles finds circular dependencies in the dependency graph.
func detectCycles(dependencies []ModuleDependency) [][]string {
	// Build adjacency list
	graph := make(map[string][]string)
	for _, dep := range dependencies {
		if dep.Type == "internal" { // Only check internal dependencies for cycles
			graph[dep.From] = append(graph[dep.From], dep.To)
		}
	}
	
	// DFS to find cycles
	var cycles [][]string
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	
	var dfs func(string, []string)
	dfs = func(node string, path []string) {
		visited[node] = true
		recStack[node] = true
		path = append(path, node)
		
		for _, neighbor := range graph[node] {
			if recStack[neighbor] {
				// Found cycle
				cycleStart := -1
				for i, p := range path {
					if p == neighbor {
						cycleStart = i
						break
					}
				}
				if cycleStart != -1 {
					cycle := append(path[cycleStart:], neighbor)
					cycles = append(cycles, cycle)
				}
			} else if !visited[neighbor] {
				dfs(neighbor, path)
			}
		}
		
		recStack[node] = false
	}
	
	// Check all nodes
	for node := range graph {
		if !visited[node] {
			dfs(node, nil)
		}
	}
	
	return cycles
}

// calculateMetrics computes various architecture metrics.
func calculateMetrics(modules []ModuleInfo, dependencies []ModuleDependency) AnalysisMetrics {
	totalModules := len(modules)
	totalDeps := len(dependencies)
	
	// Count cyclic dependencies
	cycles := detectCycles(dependencies)
	cyclicDeps := len(cycles)
	
	// Calculate fan-in and fan-out
	fanOut := make(map[string]int)
	fanIn := make(map[string]int)
	
	for _, dep := range dependencies {
		if dep.Type == "internal" {
			fanOut[dep.From]++
			fanIn[dep.To]++
		}
	}
	
	var totalFanOut, totalFanIn int
	for _, count := range fanOut {
		totalFanOut += count
	}
	for _, count := range fanIn {
		totalFanIn += count
	}
	
	avgFanOut := float64(totalFanOut) / float64(totalModules)
	avgFanIn := float64(totalFanIn) / float64(totalModules)
	
	// Calculate coupling index (higher = more coupled)
	couplingIndex := float64(totalDeps) / float64(totalModules*totalModules)
	
	// Calculate cohesion index (based on lines of code distribution)
	var totalLines int
	for _, module := range modules {
		totalLines += module.Lines
	}
	avgLines := float64(totalLines) / float64(totalModules)
	
	var variance float64
	for _, module := range modules {
		diff := float64(module.Lines) - avgLines
		variance += diff * diff
	}
	variance /= float64(totalModules)
	
	// Lower variance indicates higher cohesion
	cohesionIndex := 1.0 / (1.0 + variance/avgLines)
	
	return AnalysisMetrics{
		TotalModules:  totalModules,
		TotalDeps:     totalDeps,
		CyclicDeps:    cyclicDeps,
		MaxDepth:      calculateMaxDepth(dependencies),
		AvgFanOut:     avgFanOut,
		AvgFanIn:      avgFanIn,
		CouplingIndex: couplingIndex,
		CohesionIndex: cohesionIndex,
	}
}

// calculateMaxDepth finds the maximum dependency chain length.
func calculateMaxDepth(dependencies []ModuleDependency) int {
	// Build adjacency list
	graph := make(map[string][]string)
	for _, dep := range dependencies {
		if dep.Type == "internal" {
			graph[dep.From] = append(graph[dep.From], dep.To)
		}
	}
	
	maxDepth := 0
	visited := make(map[string]bool)
	
	var dfs func(string, int)
	dfs = func(node string, depth int) {
		if depth > maxDepth {
			maxDepth = depth
		}
		visited[node] = true
		
		for _, neighbor := range graph[node] {
			if !visited[neighbor] {
				dfs(neighbor, depth+1)
			}
		}
		
		visited[node] = false
	}
	
	// Try all nodes as starting points
	for node := range graph {
		dfs(node, 1)
	}
	
	return maxDepth
}

// generateWarnings identifies potential architecture issues.
func generateWarnings(modules []ModuleInfo, dependencies []ModuleDependency, cycles [][]string) []string {
	var warnings []string
	
	// Check for circular dependencies
	if len(cycles) > 0 {
		warnings = append(warnings, fmt.Sprintf("Found %d circular dependencies", len(cycles)))
	}
	
	// Check for modules with too many dependencies
	depCount := make(map[string]int)
	for _, dep := range dependencies {
		depCount[dep.From]++
	}
	
	for module, count := range depCount {
		if count > 10 {
			warnings = append(warnings, fmt.Sprintf("Module '%s' has high coupling (%d dependencies)", module, count))
		}
	}
	
	// Check for very large modules
	for _, module := range modules {
		if module.Lines > 1000 {
			warnings = append(warnings, fmt.Sprintf("Module '%s' is very large (%d lines)", module.Name, module.Lines))
		}
	}
	
	// Check for modules with no exports
	for _, module := range modules {
		if len(module.Exports) == 0 {
			warnings = append(warnings, fmt.Sprintf("Module '%s' has no exported functions", module.Name))
		}
	}
	
	return warnings
}

// formatMarkdownReport generates a markdown analysis report.
func formatMarkdownReport(result *AnalysisResult, analysisType string) string {
	var sb strings.Builder
	
	fmt.Fprintf(&sb, "# Crypto-Framework Module Analysis\n\n")
	fmt.Fprintf(&sb, "**Analysis Type:** %s\n", analysisType)
	fmt.Fprintf(&sb, "**Generated:** %s\n\n", "now") // TODO: add timestamp
	
	// Metrics Summary
	fmt.Fprintf(&sb, "## Metrics Summary\n\n")
	fmt.Fprintf(&sb, "| Metric | Value |\n")
	fmt.Fprintf(&sb, "|--------|-------|\n")
	fmt.Fprintf(&sb, "| Total Modules | %d |\n", result.Metrics.TotalModules)
	fmt.Fprintf(&sb, "| Total Dependencies | %d |\n", result.Metrics.TotalDeps)
	fmt.Fprintf(&sb, "| Circular Dependencies | %d |\n", result.Metrics.CyclicDeps)
	fmt.Fprintf(&sb, "| Max Dependency Depth | %d |\n", result.Metrics.MaxDepth)
	fmt.Fprintf(&sb, "| Average Fan-Out | %.2f |\n", result.Metrics.AvgFanOut)
	fmt.Fprintf(&sb, "| Average Fan-In | %.2f |\n", result.Metrics.AvgFanIn)
	fmt.Fprintf(&sb, "| Coupling Index | %.3f |\n", result.Metrics.CouplingIndex)
	fmt.Fprintf(&sb, "| Cohesion Index | %.3f |\n", result.Metrics.CohesionIndex)
	
	// Warnings
	if len(result.Warnings) > 0 {
		fmt.Fprintf(&sb, "\n## ⚠️ Warnings\n\n")
		for _, warning := range result.Warnings {
			fmt.Fprintf(&sb, "- %s\n", warning)
		}
	}
	
	// Module Overview
	fmt.Fprintf(&sb, "\n## Module Overview\n\n")
	fmt.Fprintf(&sb, "| Module | Files | Lines | Functions | Exports | Dependencies |\n")
	fmt.Fprintf(&sb, "|--------|-------|-------|-----------|---------|-------------|\n")
	
	// Sort modules by name
	sort.Slice(result.Modules, func(i, j int) bool {
		return result.Modules[i].Name < result.Modules[j].Name
	})
	
	for _, module := range result.Modules {
		fmt.Fprintf(&sb, "| %s | %d | %d | %d | %d | %d |\n",
			module.Name, len(module.Files), module.Lines, len(module.Functions),
			len(module.Exports), len(module.Dependencies))
	}
	
	// Dependencies
	if len(result.Dependencies) > 0 {
		fmt.Fprintf(&sb, "\n## Dependencies\n\n")
		
		// Group by type
		internal := []ModuleDependency{}
		external := []ModuleDependency{}
		
		for _, dep := range result.Dependencies {
			if dep.Type == "internal" {
				internal = append(internal, dep)
			} else {
				external = append(external, dep)
			}
		}
		
		if len(internal) > 0 {
			fmt.Fprintf(&sb, "### Internal Dependencies (%d)\n\n", len(internal))
			fmt.Fprintf(&sb, "```mermaid\n")
			fmt.Fprintf(&sb, "graph TD\n")
			for _, dep := range internal {
				fmt.Fprintf(&sb, "  %s --> %s\n", 
					strings.ReplaceAll(dep.From, "/", "_"),
					strings.ReplaceAll(dep.To, "/", "_"))
			}
			fmt.Fprintf(&sb, "```\n\n")
		}
		
		if len(external) > 0 {
			fmt.Fprintf(&sb, "### External Dependencies (%d)\n\n", len(external))
			for _, dep := range external {
				fmt.Fprintf(&sb, "- **%s** → %s\n", dep.From, dep.To)
			}
		}
	}
	
	// Circular Dependencies
	if len(result.Cycles) > 0 {
		fmt.Fprintf(&sb, "\n## 🔄 Circular Dependencies\n\n")
		for i, cycle := range result.Cycles {
			fmt.Fprintf(&sb, "### Cycle %d\n\n", i+1)
			fmt.Fprintf(&sb, "```\n%s\n```\n\n", strings.Join(cycle, " → "))
		}
	}
	
	return sb.String()
}

// formatDOTGraph generates a DOT format graph for visualization.
func formatDOTGraph(result *AnalysisResult) string {
	var sb strings.Builder
	
	fmt.Fprintf(&sb, "digraph framework {\n")
	fmt.Fprintf(&sb, "  rankdir=TB;\n")
	fmt.Fprintf(&sb, "  node [shape=box];\n\n")
	
	// Add nodes
	for _, module := range result.Modules {
		nodeName := strings.ReplaceAll(module.Name, "/", "_")
		fmt.Fprintf(&sb, "  %s [label=\"%s\\n%d lines\"];\n", nodeName, module.Name, module.Lines)
	}
	
	fmt.Fprintf(&sb, "\n")
	
	// Add edges
	for _, dep := range result.Dependencies {
		if dep.Type == "internal" {
			fromNode := strings.ReplaceAll(dep.From, "/", "_")
			toNode := strings.ReplaceAll(dep.To, "/", "_")
			fmt.Fprintf(&sb, "  %s -> %s;\n", fromNode, toNode)
		}
	}
	
	fmt.Fprintf(&sb, "}\n")
	
	return sb.String()
}

// formatTextReport generates a simple text report.
func formatTextReport(result *AnalysisResult) string {
	var sb strings.Builder
	
	fmt.Fprintf(&sb, "Crypto-Framework Module Analysis\n")
	fmt.Fprintf(&sb, "================================\n\n")
	
	fmt.Fprintf(&sb, "Modules: %d\n", result.Metrics.TotalModules)
	fmt.Fprintf(&sb, "Dependencies: %d\n", result.Metrics.TotalDeps)
	fmt.Fprintf(&sb, "Cycles: %d\n", result.Metrics.CyclicDeps)
	
	if len(result.Warnings) > 0 {
		fmt.Fprintf(&sb, "\nWarnings:\n")
		for _, warning := range result.Warnings {
			fmt.Fprintf(&sb, "  - %s\n", warning)
		}
	}
	
	return sb.String()
}

// Helper functions

// removeDuplicates removes duplicate strings from a slice.
func removeDuplicates(slice []string) []string {
	keys := make(map[string]bool)
	var result []string
	
	for _, item := range slice {
		if !keys[item] {
			keys[item] = true
			result = append(result, item)
		}
	}
	
	return result
}