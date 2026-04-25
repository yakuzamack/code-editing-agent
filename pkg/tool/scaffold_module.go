package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ScaffoldModuleDefinition is the definition for the scaffold_module tool.
var ScaffoldModuleDefinition = Definition{
	Name: "scaffold_module",
	Description: `Generate the full directory structure and boilerplate for a new crypto-framework implant module.

Creates: internal/implant/modules/<name>/
  ├── <name>.go           — Main implementation skeleton with module interface
  ├── <name>_windows.go   — Windows-specific platform stubs
  ├── <name>_linux.go     — Linux-specific platform stubs
  ├── <name>_darwin.go    — macOS-specific platform stubs
  └── <name>_test.go      — Test file with placeholder tests

Also updates the module registry (internal/implant/registry/registry.go) with the new module entry.
Uses existing module patterns (logger, config, error handling) consistent with the framework.`,
	InputSchema: GenerateSchema[ScaffoldModuleInput](),
	Function:    ExecuteScaffoldModule,
}

// ScaffoldModuleInput is the input for the scaffold_module tool.
type ScaffoldModuleInput struct {
	// Name is the module name in snake_case (e.g., "token_sniffer").
	Name string `json:"name" jsonschema:"description=Module name in snake_case (e.g., token_sniffer). This becomes the directory and package name."`

	// DisplayName is a human-readable title (e.g., "Token Sniffer"). Auto-derived from Name if empty.
	DisplayName string `json:"display_name,omitempty" jsonschema:"description=Human-readable title (e.g., Token Sniffer). Auto-derived from Name if empty."`

	// Description is a brief summary of what the module does.
	Description string `json:"description" jsonschema:"description=Brief summary of what the module does. Used in module comments and registry."`

	// Platform list defaults to "windows,linux,darwin" if empty.
	Platforms []string `json:"platforms,omitempty" jsonschema:"description=Target platforms (e.g., [\"windows\",\"linux\",\"darwin\"]). Defaults to all three."`

	// FrameworkRoot overrides auto-detection of the crypto-framework directory.
	FrameworkRoot string `json:"framework_root,omitempty" jsonschema:"description=Override auto-detection of crypto-framework root directory. Defaults to LLM_WORKDIR env or working directory."`

	// DryRun previews files without writing them.
	DryRun bool `json:"dry_run,omitempty" jsonschema:"description=If true, show what would be created without writing any files."`
}

// ExecuteScaffoldModule generates the module scaffold.
func ExecuteScaffoldModule(input json.RawMessage) (string, error) {
	var args ScaffoldModuleInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required (snake_case, e.g., token_sniffer)")
	}
	if args.Description == "" {
		return "", fmt.Errorf("description is required")
	}

	// Resolve framework root
	fwRoot := args.FrameworkRoot
	if fwRoot == "" {
		fwRoot = os.Getenv("LLM_WORKDIR")
	}
	if fwRoot == "" {
		fwRoot = WorkingDir()
	}

	// Validate framework root has the expected structure
	modulesDir := filepath.Join(fwRoot, "internal", "implant", "modules")
	if info, err := os.Stat(modulesDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("framework modules directory not found at %s — set LLM_WORKDIR or framework_root", modulesDir)
	}

	// Default platforms
	platforms := args.Platforms
	if len(platforms) == 0 {
		platforms = []string{"windows", "linux", "darwin"}
	}

	// Derive display name from snake_case if not provided
	displayName := args.DisplayName
	if displayName == "" {
		parts := strings.Split(args.Name, "_")
		for i, p := range parts {
			if len(p) > 0 {
				parts[i] = strings.ToUpper(p[:1]) + p[1:]
			}
		}
		displayName = strings.Join(parts, " ")
	}

	moduleDir := filepath.Join(modulesDir, args.Name)

	// Collect all files to create
	type fileSpec struct {
		path    string
		content string
	}

	var files []fileSpec

	// Detect module path from go.mod
	modulePath, err := detectModulePath(fwRoot)
	if err != nil {
		return "", fmt.Errorf("failed to detect module path: %w", err)
	}

	// Main module file: <name>.go
	mainFile := filepath.Join(moduleDir, args.Name+".go")
	mainContent := scaffoldMainGo(args.Name, displayName, args.Description, modulePath)
	files = append(files, fileSpec{path: mainFile, content: mainContent})

	// Platform-specific files
	for _, platform := range platforms {
		pfFile := filepath.Join(moduleDir, fmt.Sprintf("%s_%s.go", args.Name, platform))
		pfContent := scaffoldPlatformGo(args.Name, platform)
		files = append(files, fileSpec{path: pfFile, content: pfContent})
	}

	// Test file
	testFile := filepath.Join(moduleDir, args.Name+"_test.go")
	testContent := scaffoldTestGo(args.Name, displayName)
	files = append(files, fileSpec{path: testFile, content: testContent})

	// If dry run, just list what would be created
	if args.DryRun {
		var sb strings.Builder
		fmt.Fprintf(&sb, "## Scaffold Preview: %s\n\n", displayName)
		fmt.Fprintf(&sb, "**Module:** `%s`\n", args.Name)
		fmt.Fprintf(&sb, "**Directory:** `%s`\n\n", moduleDir)
		fmt.Fprintf(&sb, "**Files to create:**\n\n")
		for _, f := range files {
			rel, _ := filepath.Rel(fwRoot, f.path)
			fmt.Fprintf(&sb, "  - `%s` (%d lines)\n", filepath.ToSlash(rel), strings.Count(f.content, "\n"))
		}
		fmt.Fprintf(&sb, "\n**Platform files:** %s\n", strings.Join(platforms, ", "))
		fmt.Fprintf(&sb, "**Description:** %s\n", args.Description)
		return sb.String(), nil
	}

	// Create directory
	if err := os.MkdirAll(moduleDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create module directory %s: %w", moduleDir, err)
	}

	// Write files
	created := []string{}
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte(f.content), 0644); err != nil {
			return "", fmt.Errorf("failed to write %s: %w", f.path, err)
		}
		rel, _ := filepath.Rel(fwRoot, f.path)
		created = append(created, filepath.ToSlash(rel))
	}

	// Try to update registry if it exists
	registryUpdated := false
	registryPath := filepath.Join(fwRoot, "internal", "implant", "registry", "registry.go")
	if data, err := os.ReadFile(registryPath); err == nil {
		newData := scaffoldRegistryEntry(args.Name, displayName, args.Description, string(data))
		if newData != string(data) {
			if err := os.WriteFile(registryPath, []byte(newData), 0644); err == nil {
				registryUpdated = true
			}
		}
	}

	// Build result summary
	var sb strings.Builder
	fmt.Fprintf(&sb, "✅ Scaffold created for **%s**\n\n", displayName)
	fmt.Fprintf(&sb, "**Directory:** `internal/implant/modules/%s/`\n\n", args.Name)
	fmt.Fprintf(&sb, "**Files created:**\n")
	for _, c := range created {
		fmt.Fprintf(&sb, "  - `%s`\n", c)
	}
	if registryUpdated {
		fmt.Fprintf(&sb, "\n✅ Registry updated: `internal/implant/registry/registry.go`\n")
	} else {
		fmt.Fprintf(&sb, "\nℹ️  No registry file found — you may need to register the module manually.\n")
	}
	fmt.Fprintf(&sb, "\n**Next steps:**\n")
	fmt.Fprintf(&sb, "  1. Implement the `Run()` method in `%s.go`\n", args.Name)
	fmt.Fprintf(&sb, "  2. Add platform-specific logic in `%s_*.go` files\n", args.Name)
	fmt.Fprintf(&sb, "  3. Write unit tests in `%s_test.go`\n", args.Name)
	fmt.Fprintf(&sb, "  4. Run `framework_status` to update the project health report\n")

	return sb.String(), nil
}

// detectModulePath reads go.mod to find the module path.
func detectModulePath(frameworkRoot string) (string, error) {
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

// scaffoldMainGo generates the main module implementation file.
func scaffoldMainGo(name, displayName, description, modulePath string) string {
	pkgName := strings.ReplaceAll(name, "-", "")

	var sb strings.Builder
	//nolint:staticcheck // Complex multiline template - using fmt.Sprintf for readability
	sb.WriteString(fmt.Sprintf(`// Package %s provides the %s module.
//
// %s
package %s

import (
	"context"
	"fmt"

	"%s/internal/implant/config"
	"%s/internal/implant/logger"
)

// %sModule is the main module struct.
type %sModule struct {
	logger  *logger.Logger
	config  *config.ModuleConfig
	implant interface{} // Weak reference to avoid circular import
}

// New%sModule creates a new %s module.
func New%sModule(implant interface{}, cfg *config.ModuleConfig) *%sModule {
	return &%sModule{
		logger:  logger.Get(),
		config:  cfg,
		implant: implant,
	}
}

// Name returns the module name.
func (m *%sModule) Name() string {
	return "%s"
}

// Description returns a brief description of what this module does.
func (m *%sModule) Description() string {
	return "%s"
}

// Run executes the module's main logic.
// This is the entry point called by the implant scheduler.
func (m *%sModule) Run(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	m.logger.Info("module", "%s", "msg", "Run() called")
	return nil, fmt.Errorf("module %s is not yet implemented")
}
`,
		pkgName, displayName,
		description,
		pkgName,
		modulePath, modulePath,
		displayName, displayName,
		displayName, displayName, displayName, displayName, displayName,
		name,
		displayName,
		description,
		displayName, name, displayName, displayName,
	))

	return sb.String()
}

// scaffoldPlatformGo generates a platform-specific stubs file.
func scaffoldPlatformGo(name, platform string) string {
	pkgName := strings.ReplaceAll(name, "-", "")

	var buildTag string
	switch platform {
	case "windows":
		buildTag = "windows"
	case "linux":
		buildTag = "linux"
	case "darwin":
		buildTag = "darwin"
	default:
		buildTag = platform
	}

	var sb strings.Builder
	//nolint:staticcheck // Complex multiline template - using fmt.Sprintf for readability
	sb.WriteString(fmt.Sprintf(`//go:build %s

package %s

import (
	"context"
	"fmt"
)

// platformRun executes the platform-specific implementation for %s.
// This file is only compiled on %s targets.
func (m *%sModule) platformRun(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	// TODO: Implement %s-specific logic for module %s
	return nil, fmt.Errorf("%s: %s platform not yet implemented")
}
`,
		buildTag,
		pkgName,
		platform, buildTag,
		displayName(pkgName),
		buildTag, name,
		buildTag, name,
	))

	return sb.String()
}

// scaffoldTestGo generates a test file.
func scaffoldTestGo(name, displayName string) string {
	pkgName := strings.ReplaceAll(name, "-", "")

	var sb strings.Builder
	//nolint:staticcheck // Complex multiline template - using fmt.Sprintf for readability
	sb.WriteString(fmt.Sprintf(`package %s

import (
	"context"
	"testing"
)

func TestNew%sModule(t *testing.T) {
	// TODO: Write proper test with mock implant and config
	_ = context.Background()
	t.Log("New%sModule: placeholder test — implement when module is ready")
}

func Test%sModule_Name(t *testing.T) {
	t.Log("Name(): placeholder test — implement when module is ready")
}

func Test%sModule_Description(t *testing.T) {
	t.Log("Description(): placeholder test — implement when module is ready")
}
`,
		pkgName,
		displayName, displayName, displayName, displayName,
	))

	return sb.String()
}

// scaffoldRegistryEntry adds a module entry to the registry file.
func scaffoldRegistryEntry(name, displayName, description, regContent string) string {
	// Find the last module entry and insert before the closing of the list
	// Look for a pattern like "// ModuleName" or "registry.Add("
	moduleVar := fmt.Sprintf(" \"%s\": New%sModule,", name, displayName)

	insertion := fmt.Sprintf(`	// %s — %s
	"%s": New%sModule,`,
		displayName, description, name, displayName)

	// Insert after the last "module" entry (before closing brace of the map)
	// Look for the last line that matches `"...": New...Module,`
	lines := strings.Split(regContent, "\n")
	lastEntryIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, `"`) && strings.Contains(trimmed, ": New") && strings.HasSuffix(trimmed, "Module,") {
			lastEntryIdx = i
		}
	}

	if lastEntryIdx < 0 {
		return regContent // Can't find insertion point, skip
	}

	// Insert after the last entry
	result := make([]string, 0, len(lines)+1)
	result = append(result, lines[:lastEntryIdx+1]...)
	result = append(result, insertion)
	result = append(result, lines[lastEntryIdx+1:]...)

	// Check if already registered
	if strings.Contains(regContent, moduleVar) {
		return regContent // Already exists
	}

	return strings.Join(result, "\n")
}

// displayName converts PascalCase or snake_case to a readable display name.
func displayName(s string) string {
	// Already pascal-case-like
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}
