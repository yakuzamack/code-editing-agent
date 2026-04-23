package tool

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// SmartReadFileDefinition is the definition for the smart_read_file tool.
var SmartReadFileDefinition = Definition{
	Name:        "smart_read_file",
	Description: "Intelligently read a file by extracting only relevant code sections. Provides 4 strategies: (1) extract by symbol name (function/type/method/constant), (2) read exact line range, (3) file summary listing all top-level symbols, (4) full file with truncation. Headers show line numbers and file stats. Always prefer this over plain read_file for large files.",
	InputSchema: GenerateSchema[SmartReadFileInput](),
	Function:    SmartReadFile,
}

// SmartReadFileInput allows flexible file reading strategies.
type SmartReadFileInput struct {
	// Path to the file (required)
	Path string `json:"path" jsonschema:"description=Relative path to the file to read"`

	// Symbol: extract only this function/type/method/const (e.g., Decrypt, validateInput, Config)
	Symbol string `json:"symbol" jsonschema:"description=Optional: extract only this symbol (function, type, method, const). Much faster for large files. Falls back to full file if not found."`

	// LineStart/LineEnd: read only lines in range (1-indexed, inclusive)
	LineStart int `json:"line_start" jsonschema:"description=Optional: start line number (1-indexed). Combine with line_end to read a range."`
	LineEnd   int `json:"line_end" jsonschema:"description=Optional: end line number (1-indexed, inclusive). Use with line_start for range reads."`

	// Summary: if true, show only the file outline (top-level symbol list) instead of full content
	Summary bool `json:"summary" jsonschema:"description=Optional: if true, only show the file outline (function signatures, types, constants, vars). Use this to survey a file before reading specific symbols. Default false."`

	// MaxLines: truncate output if larger (prevent token bloat)
	MaxLines int `json:"max_lines" jsonschema:"description=Optional: maximum lines to return (default 500). Prevents truncating large symbols."`

	// ContextLines: lines of surrounding context when extracting a symbol (default 0)
	ContextLines int `json:"context_lines" jsonschema:"description=Optional: number of lines of surrounding context to include when extracting a symbol. Default 0."`
}

// topLevelSymbol represents a parsed top-level declaration in a Go file.
type topLevelSymbol struct {
	Kind     string // func, type, const, var, struct, interface
	Name     string
	Line     int
	Snapshot string // first line of the declaration
}

// regex patterns for Go top-level declarations
var (
	reFunc      = regexp.MustCompile(`^func\s+(\([^)]*\)\s*)?([A-Za-z_]\w*)\s*\(`)
	reType      = regexp.MustCompile(`^type\s+([A-Za-z_]\w*)`)
	reConst     = regexp.MustCompile(`^const\s+([A-Za-z_]\w*)`)
	reVar       = regexp.MustCompile(`^var\s+([A-Za-z_]\w*)`)
	reStruct    = regexp.MustCompile(`^type\s+([A-Za-z_]\w*)\s+struct`)
	reInterface = regexp.MustCompile(`^type\s+([A-Za-z_]\w*)\s+interface`)
)

// SmartReadFile implements the smart_read_file tool.
func SmartReadFile(input json.RawMessage) (string, error) {
	var params SmartReadFileInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("smart_read_file: invalid input: %w", err)
	}

	if params.Path == "" {
		return "", fmt.Errorf("smart_read_file: path is required")
	}

	resolvedPath, err := resolvePath(params.Path)
	if err != nil {
		return "", fmt.Errorf("smart_read_file: %w", err)
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("smart_read_file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("smart_read_file: %q is a directory, not a file", params.Path)
	}

	// Read all lines
	file, err := os.Open(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("smart_read_file: %w", err)
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("smart_read_file: error reading file: %w", err)
	}

	if len(lines) == 0 {
		return fmt.Sprintf("File: %s (empty)\n", displayPath(resolvedPath)), nil
	}

	// Apply maxLines default
	maxLines := params.MaxLines
	if maxLines <= 0 {
		maxLines = 500
	}

	// Strategy 1: Summary mode
	if params.Summary {
		return buildFileSummary(resolvedPath, lines)
	}

	// Strategy 2: Symbol extraction
	if params.Symbol != "" {
		return extractSymbol(resolvedPath, lines, params.Symbol, params.ContextLines)
	}

	// Strategy 3: Line range
	if params.LineStart > 0 || params.LineEnd > 0 {
		return readLineRange(resolvedPath, lines, params.LineStart, params.LineEnd, maxLines)
	}

	// Strategy 4: Full file with truncation
	return readFullFile(resolvedPath, lines, maxLines)
}

// buildFileSummary returns a file outline with all top-level declarations.
func buildFileSummary(filePath string, lines []string) (string, error) {
	var symbols []topLevelSymbol
	displayPath := displayPath(filePath)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}

		// Check for struct/interface first (more specific)
		if m := reInterface.FindStringSubmatch(trimmed); m != nil {
			symbols = append(symbols, topLevelSymbol{Kind: "interface", Name: m[1], Line: i + 1, Snapshot: trimmed})
			continue
		}
		if m := reStruct.FindStringSubmatch(trimmed); m != nil {
			symbols = append(symbols, topLevelSymbol{Kind: "struct", Name: m[1], Line: i + 1, Snapshot: trimmed})
			continue
		}
		if m := reType.FindStringSubmatch(trimmed); m != nil {
			symbols = append(symbols, topLevelSymbol{Kind: "type", Name: m[1], Line: i + 1, Snapshot: trimmed})
			continue
		}
		if m := reFunc.FindStringSubmatch(trimmed); m != nil {
			name := m[2]
			symbols = append(symbols, topLevelSymbol{Kind: "func", Name: name, Line: i + 1, Snapshot: trimmed})
			continue
		}
		if m := reConst.FindStringSubmatch(trimmed); m != nil {
			symbols = append(symbols, topLevelSymbol{Kind: "const", Name: m[1], Line: i + 1, Snapshot: trimmed})
			continue
		}
		if m := reVar.FindStringSubmatch(trimmed); m != nil {
			symbols = append(symbols, topLevelSymbol{Kind: "var", Name: m[1], Line: i + 1, Snapshot: trimmed})
			continue
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "File: %s  (%d lines, %d symbols)\n\n", displayPath, len(lines), len(symbols))

	// Group by kind
	type groupEntry struct {
		kind    string
		symbols []topLevelSymbol
	}
	groups := []groupEntry{
		{"func", nil},
		{"struct", nil},
		{"interface", nil},
		{"type", nil},
		{"const", nil},
		{"var", nil},
	}
	for _, s := range symbols {
		for i, g := range groups {
			if g.kind == s.Kind {
				groups[i].symbols = append(groups[i].symbols, s)
				break
			}
		}
	}

	for _, g := range groups {
		if len(g.symbols) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "  %s:\n", g.kind)
		for _, s := range g.symbols {
			fmt.Fprintf(&sb, "    L%5d  %s\n", s.Line, s.Snapshot)
		}
	}

	return sb.String(), nil
}

// extractSymbol extracts a specific symbol (function, type, etc.) from the file.
func extractSymbol(filePath string, lines []string, symbol string, contextLines int) (string, error) {
	displayPath := displayPath(filePath)

	// Find the symbol start line
	symbolStart := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Match: func Foo, func (r *T) Foo, type Foo, type Foo struct, type Foo interface, const Foo, var Foo
		patterns := []string{
			`^func\s+(\([^)]*\)\s*)?` + regexp.QuoteMeta(symbol) + `\s*\(`,
			`^func\s+(\([^)]*\)\s*)?` + regexp.QuoteMeta(symbol) + `\s+`,
			`^type\s+` + regexp.QuoteMeta(symbol) + `\b`,
			`^const\s+` + regexp.QuoteMeta(symbol) + `\b`,
			`^var\s+` + regexp.QuoteMeta(symbol) + `\b`,
		}
		for _, pat := range patterns {
			matched, _ := regexp.MatchString(pat, trimmed)
			if matched {
				symbolStart = i
				break
			}
		}
		if symbolStart >= 0 {
			break
		}
	}

	if symbolStart < 0 {
		// Fallback: search for symbol as a standalone word in the line
		for i, line := range lines {
			if strings.Contains(line, symbol) {
				symbolStart = i
				break
			}
		}
	}

	if symbolStart < 0 {
		return fmt.Sprintf("Symbol %q not found in %s", symbol, displayPath), nil
	}

	// Determine end: next top-level declaration at same or lesser indentation, or EOF
	symbolEnd := len(lines)
	for i := symbolStart + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		// Check if this line starts a new top-level declaration
		if !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") && !strings.HasPrefix(trimmed, "*") {
			if regexp.MustCompile(`^(func\s+|type\s+|const\s+|var\s+)`).MatchString(trimmed) {
				// Only stop if it's at the same or lesser indentation
				indent := len(lines[i]) - len(strings.TrimLeft(lines[i], "\t "))
				startIndent := len(lines[symbolStart]) - len(strings.TrimLeft(lines[symbolStart], "\t "))
				if indent <= startIndent {
					symbolEnd = i
					break
				}
			}
		}
	}

	// Apply context before start
	startLine := symbolStart - contextLines
	if startLine < 0 {
		startLine = 0
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "File: %s  Symbol: %s  Lines %d-%d (of %d)\n\n", displayPath, symbol, startLine+1, symbolEnd, len(lines))

	for i, line := range lines[startLine:symbolEnd] {
		absLine := startLine + i + 1
		marker := " "
		if absLine >= symbolStart+1 && absLine <= symbolEnd {
			marker = ">" // highlight extracted region
		}
		if contextLines > 0 && (absLine == symbolStart+1 || absLine == symbolStart+contextLines+1) {
			// context boundary
			fmt.Fprintf(&sb, "%s L%5d  %s\n", marker, absLine, line)
		} else if absLine >= symbolStart+1 && absLine <= symbolEnd {
			fmt.Fprintf(&sb, "%s L%5d  %s\n", marker, absLine, line)
		} else {
			fmt.Fprintf(&sb, "  L%5d  %s\n", absLine, line)
		}
	}

	return sb.String(), nil
}

// readLineRange returns a specific line range with line numbers.
func readLineRange(filePath string, lines []string, lineStart, lineEnd, maxLines int) (string, error) {
	displayPath := displayPath(filePath)

	totalLines := len(lines)

	// Validate and normalize
	if lineStart <= 0 {
		lineStart = 1
	}
	if lineEnd <= 0 || lineEnd > totalLines {
		lineEnd = totalLines
	}
	if lineStart > lineEnd || lineStart > totalLines {
		return "", fmt.Errorf("smart_read_file: invalid line range %d-%d (file has %d lines)", lineStart, lineEnd, totalLines)
	}

	// Convert to 0-indexed
	start := lineStart - 1
	end := lineEnd

	// Truncate to maxLines
	if end-start > maxLines {
		end = start + maxLines
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "File: %s  Lines %d-%d (of %d)\n\n", displayPath, lineStart, end, totalLines)

	for i, line := range lines[start:end] {
		absLine := start + i + 1
		fmt.Fprintf(&sb, "L%5d  %s\n", absLine, line)
	}

	if end < totalLines && end-start == maxLines {
		fmt.Fprintf(&sb, "\n... truncated at %d lines (file has %d total) ...\n", maxLines, totalLines)
	}

	return sb.String(), nil
}

// readFullFile returns the full file with truncation.
func readFullFile(filePath string, lines []string, maxLines int) (string, error) {
	displayPath := displayPath(filePath)
	totalLines := len(lines)

	limit := maxLines
	if limit > totalLines {
		limit = totalLines
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "File: %s  (%d lines)\n\n", displayPath, totalLines)

	for i, line := range lines[:limit] {
		fmt.Fprintf(&sb, "L%5d  %s\n", i+1, line)
	}

	if totalLines > limit {
		fmt.Fprintf(&sb, "\n... truncated at %d lines (file has %d total). Use symbol= or line_start/line_end to read specific sections. ...\n", limit, totalLines)
	}

	return sb.String(), nil
}


