package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FixCompileErrorsDefinition is the definition for the fix_compile_errors tool.
var FixCompileErrorsDefinition = Definition{
	Name: "fix_compile_errors",
	Description: `Run 'go build' on the crypto-framework, parse all compiler errors, read the offending files, and suggest fixes in a single workflow.

Workflow:
  1. Runs "go build ./..." or "go build <package>" on the framework
  2. Parses all compiler errors (type mismatches, undefined vars, import issues)
  3. Reads the source lines around each error
  4. Returns a structured report with error details, surrounding code, and suggested fix patterns

Use this instead of manually running build → reading output → opening files. Saves 3-5 edit cycles per error.`,
	InputSchema: GenerateSchema[FixCompileErrorsInput](),
	Function:    ExecuteFixCompileErrors,
}

// FixCompileErrorsInput is the input for the fix_compile_errors tool.
type FixCompileErrorsInput struct {
	// Package is the Go package to build. Defaults to "./..." (all packages).
	Package string `json:"package,omitempty" jsonschema:"description=The Go package to build (e.g., './internal/implant/...'). Defaults to './...' (all packages)."`

	// FrameworkRoot overrides auto-detection of the crypto-framework directory.
	FrameworkRoot string `json:"framework_root,omitempty" jsonschema:"description=Override auto-detection of crypto-framework root. Defaults to LLM_WORKDIR env or working directory."`

	// MaxErrors caps the number of errors to process. Defaults to 10.
	MaxErrors int `json:"max_errors,omitempty" jsonschema:"description=Maximum number of errors to analyze. Defaults to 10."`

	// ContextLines is how many lines of surrounding source to show per error. Defaults to 3.
	ContextLines int `json:"context_lines,omitempty" jsonschema:"description=Number of lines of surrounding source to show per error. Defaults to 3."`

	// FixMode controls how aggressively to auto-fix errors.
	// "report" — show errors with context (default)
	// "hint"   — report + suggest specific fix strategies
	FixMode string `json:"fix_mode,omitempty" jsonschema:"description=Fix mode: 'report' (show errors with context) or 'hint' (report + suggest fixes). Defaults to 'hint'."`
}

// compileError represents a single parsed compiler error.
type compileError struct {
	File    string
	Line    int
	Column  int
	Message string
	Kind    string // "type", "undeclared", "import", "syntax", "other"
}

var (
	// Pattern for Go compiler errors: file.go:line:col: message
	goErrorRe = regexp.MustCompile(`^([^:]+):(\d+):(\d+):\s+(.+)$`)

	// Pattern for Go compiler errors without column: file.go:line: message
	goErrorNoColRe = regexp.MustCompile(`^([^:]+):(\d+):\s+(.+)$`)

	// Common error patterns for classification
	typeMismatchRe = regexp.MustCompile(`cannot use|type mismatch|cannot convert|incompatible type|does not implement|wrong type for`)
	undeclaredRe   = regexp.MustCompile(`undefined|undeclared|not declared|not a type|unresolved reference`)
	importRe       = regexp.MustCompile(`imported and not used|import cycle|expected 'import'|could not import|module .* not found|no required module`)
	syntaxRe       = regexp.MustCompile(`expected |unexpected |syntax error|missing `)
)

// compileErrorWithContext pairs a parsed error with its source context lines.
type compileErrorWithContext struct {
	compileError
	SourceLines []string
}

// ExecuteFixCompileErrors runs build and analyzes errors.
func ExecuteFixCompileErrors(input json.RawMessage) (string, error) {
	var args FixCompileErrorsInput
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

	// Validate it's a Go project
	goModPath := filepath.Join(fwRoot, "go.mod")
	if _, err := os.Stat(goModPath); err != nil {
		return "", fmt.Errorf("no go.mod found at %s — set framework_root or LLM_WORKDIR to the crypto-framework directory", fwRoot)
	}

	maxErrors := args.MaxErrors
	if maxErrors <= 0 {
		maxErrors = 10
	}
	if maxErrors > 50 {
		maxErrors = 50
	}

	ctxLines := args.ContextLines
	if ctxLines <= 0 {
		ctxLines = 3
	}
	if ctxLines > 20 {
		ctxLines = 20
	}

	// Step 1: Run go build
	pkg := args.Package
	if pkg == "" {
		pkg = "./..."
	}

	buildCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(buildCtx, "go", "build", pkg)
	cmd.Dir = fwRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	buildOutput := stderr.String()
	if buildOutput == "" {
		buildOutput = stdout.String()
	}

	// If build succeeded, report that
	if err == nil {
		return "✅ Build succeeded with no errors.", nil
	}

	if buildCtx.Err() == context.DeadlineExceeded {
		return "⚠️ Build timed out after 120s. Try building a more specific package.", nil
	}

	// Step 2: Parse compiler errors
	parsedErrors := parseCompileErrors(buildOutput, maxErrors)

	if len(parsedErrors) == 0 {
		// Unparseable output — return raw
		return fmt.Sprintf("Build failed with unparseable output:\n\n```\n%s\n```", truncateOutput(buildOutput, 8000)), nil
	}

	// Step 3: Read source context for each error
	var enriched []compileErrorWithContext
	for _, e := range parsedErrors {
		lines := readSourceLines(fwRoot, e.File, e.Line, ctxLines)
		enriched = append(enriched, compileErrorWithContext{
			compileError: e,
			SourceLines:  lines,
		})
	}

	// Step 4: Build report
	return formatCompileErrorReport(fwRoot, enriched, args.FixMode), nil
}

// parseCompileErrors extracts structured errors from go build output.
func parseCompileErrors(output string, maxResults int) []compileError {
	lines := strings.Split(output, "\n")
	var errors []compileError
	seen := make(map[string]bool) // dedup by file:line:message

	for _, line := range lines {
		if len(errors) >= maxResults {
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		var file string
		var lineNum int
		var col int
		var message string

		// Try with column
		if m := goErrorRe.FindStringSubmatch(trimmed); m != nil {
			file = m[1]
			lineNum, _ = strconv.Atoi(m[2])
			col, _ = strconv.Atoi(m[3])
			message = strings.TrimSpace(m[4])
		} else if m := goErrorNoColRe.FindStringSubmatch(trimmed); m != nil {
			file = m[1]
			lineNum, _ = strconv.Atoi(m[2])
			message = strings.TrimSpace(m[3])
		} else {
			continue
		}

		// Skip non-file errors (like "# package/name" headers)
		if !strings.HasSuffix(file, ".go") {
			continue
		}

		key := fmt.Sprintf("%s:%d:%s", file, lineNum, message)
		if seen[key] {
			continue
		}
		seen[key] = true

		// Classify the error
		kind := classifyError(message)

		errors = append(errors, compileError{
			File:    file,
			Line:    lineNum,
			Column:  col,
			Message: message,
			Kind:    kind,
		})
	}

	return errors
}

// classifyError categorizes a compiler error message.
func classifyError(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case typeMismatchRe.MatchString(lower):
		return "type"
	case undeclaredRe.MatchString(lower):
		return "undeclared"
	case importRe.MatchString(lower):
		return "import"
	case syntaxRe.MatchString(lower):
		return "syntax"
	default:
		return "other"
	}
}

// readSourceLines reads context lines around an error from the source file.
// Returns the lines with line numbers prepended.
func readSourceLines(fwRoot, file string, errLine, contextLines int) []string {
	// Resolve relative to framework root
	fullPath := file
	if !filepath.IsAbs(file) {
		fullPath = filepath.Join(fwRoot, file)
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		// Try just the base name joined with fwRoot
		fullPath = filepath.Join(fwRoot, filepath.Base(file))
		data, err = os.ReadFile(fullPath)
		if err != nil {
			return []string{fmt.Sprintf("[cannot read file: %s]", fullPath)}
		}
	}

	allLines := strings.Split(string(data), "\n")

	start := errLine - contextLines - 1 // 0-indexed
	if start < 0 {
		start = 0
	}
	end := errLine + contextLines
	if end > len(allLines) {
		end = len(allLines)
	}

	var result []string
	for i := start; i < end; i++ {
		lineNum := i + 1
		marker := " "
		if lineNum == errLine {
			marker = "→"
		}
		result = append(result, fmt.Sprintf("  %s %4d: %s", marker, lineNum, allLines[i]))
	}

	return result
}

// formatCompileErrorReport builds the final report.
func formatCompileErrorReport(fwRoot string, errors []compileErrorWithContext, fixMode string) string {
	var sb strings.Builder

	sb.WriteString("## ⚠️ Compile Errors Detected\n\n")
	sb.WriteString(fmt.Sprintf("Found **%d** error(s):\n\n", len(errors)))

	// Group by file
	byFile := make(map[string][]compileErrorWithContext)
	for _, e := range errors {
		byFile[e.File] = append(byFile[e.File], e)
	}

	// Sort file names
	var fileNames []string
	for f := range byFile {
		fileNames = append(fileNames, f)
	}
	sort.Strings(fileNames)

	for _, file := range fileNames {
		fileErrors := byFile[file]
		relFile := filepath.ToSlash(file)

		fmt.Fprintf(&sb, "### 📄 `%s` (%d error%s)\n\n", relFile, len(fileErrors),
			map[bool]string{true: "s", false: ""}[len(fileErrors) != 1])

		for _, e := range fileErrors {
			// Error summary
			kindEmoji := map[string]string{
				"type":       "🔤",
				"undeclared": "❓",
				"import":     "📦",
				"syntax":     "🔧",
				"other":      "⚠️",
			}
			emoji := kindEmoji[e.Kind]
			if emoji == "" {
				emoji = "⚠️"
			}

			colStr := ""
			if e.Column > 0 {
				colStr = fmt.Sprintf(":%d", e.Column)
			}
			fmt.Fprintf(&sb, "%s **Line %d%s** — %s\n", emoji, e.Line, colStr, e.Message)

			// Source context
			if len(e.SourceLines) > 0 {
				sb.WriteString("\n```go\n")
				for _, line := range e.SourceLines {
					sb.WriteString(line)
					sb.WriteString("\n")
				}
				sb.WriteString("```\n")
			}

			// Fix hints
			if fixMode == "hint" || fixMode == "" {
				hint := generateFixHint(e.compileError)
				if hint != "" {
					fmt.Fprintf(&sb, "\n💡 *%s*\n", hint)
				}
			}

			sb.WriteString("\n")
		}
	}

	// Add build command
	fmt.Fprintf(&sb, "---\n\n**To retry:** `go build %s`\n", filepath.ToSlash(errors[0].File))

	return sb.String()
}

// generateFixHint provides a suggested fix strategy based on error type.
func generateFixHint(err compileError) string {
	switch err.Kind {
	case "type":
		return fmt.Sprintf("Type mismatch on line %d. Check the function signature and variable types. You may need to cast the value or change the expected type.", err.Line)
	case "undeclared":
		return fmt.Sprintf("Undefined symbol on line %d. Check for typos, missing imports, or an unexported name. If it's a new function, make sure it's exported (capitalized).", err.Line)
	case "import":
		return fmt.Sprintf("Import issue on line %d. Run `go mod tidy` to sync dependencies, or check the import path for typos. Remove unused imports if this is not a new dependency.", err.Line)
	case "syntax":
		return fmt.Sprintf("Syntax error around line %d. Check for missing brackets, parentheses, or commas. Also verify struct literals and function calls have correct delimiters.", err.Line)
	default:
		msg := strings.ToLower(err.Message)
		if strings.Contains(msg, "used as value") {
			return "This function or expression returns a value but is being used as a statement. Use `_ = fn()` or assign the result."
		}
		if strings.Contains(msg, "declared and not used") {
			return "This variable is declared but never read. Either use it or replace with `_`."
		}
		if strings.Contains(msg, "missing return") {
			return "This function has a code path that doesn't return a value. Add a return statement at the end."
		}
		return ""
	}
}
