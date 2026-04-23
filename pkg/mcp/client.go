package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LSPClient manages a gopls JSON-RPC LSP session over stdin/stdout.
type LSPClient struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	reader   *bufio.Reader
	mu       sync.Mutex
	msgID    int
	rootPath string
}

// lspRequest is a JSON-RPC 2.0 request.
type lspRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// lspNotification is a JSON-RPC 2.0 notification (no ID).
type lspNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type lspError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// lspPosition represents a zero-indexed position in a file.
type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// lspLocation represents a file location with a range.
type lspLocation struct {
	URI   string    `json:"uri"`
	Range lspRange  `json:"range"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

// lspTextDocumentItem is the text document identifier.
type lspTextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// lspTextDocumentPositionParams combines text document + position.
type lspTextDocumentPositionParams struct {
	TextDocument lspTextDocumentIdentifier `json:"textDocument"`
	Position     lspPosition               `json:"position"`
}

// lspReferenceParams includes context for reference requests.
type lspReferenceParams struct {
	TextDocument lspTextDocumentIdentifier `json:"textDocument"`
	Position     lspPosition               `json:"position"`
	Context      lspReferenceContext       `json:"context"`
}

type lspReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// lspHover represents a hover response.
type lspHover struct {
	Contents lspMarkupContent `json:"contents"`
	Range    *lspRange        `json:"range,omitempty"`
}

type lspMarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// lspRenameParams for rename requests.
type lspRenameParams struct {
	TextDocument lspTextDocumentIdentifier `json:"textDocument"`
	Position     lspPosition               `json:"position"`
	NewName      string                    `json:"newName"`
}

// lspWorkspaceEdit is the result of a rename.
type lspWorkspaceEdit struct {
	Changes map[string][]lspTextEdit `json:"changes,omitempty"`
}

type lspTextEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// lspDiagnosticParams for publishing diagnostics.
type lspPublishDiagnosticsParams struct {
	URI         string         `json:"uri"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity,omitempty"`
	Message  string   `json:"message"`
	Source   string   `json:"source,omitempty"`
}

// filePathToURI converts a local file path to a file:// URI.
func filePathToURI(absPath string) string {
	return "file://" + absPath
}

// uriToFilePath strips file:// prefix.
func uriToFilePath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}

// Client manages connections to external servers (gopls, etc.)
type Client struct {
	goplsPath string
	timeout   time.Duration
	active    bool
	lsp       *LSPClient // active LSP session
}

// NewClient creates a new external server client.
func NewClient(timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		goplsPath: "gopls",
		timeout:   timeout,
		active:    false,
	}
}

// StartServer starts a gopls LSP process and initializes it.
func (c *Client) StartServer(ctx context.Context, name string, cmd string, args ...string) error {
	if name != "go-lsp" {
		return fmt.Errorf("unsupported server: %q", name)
	}

	cmdPath, err := exec.LookPath(cmd)
	if err != nil {
		if cmd == "gopls" {
			homeDir, _ := os.UserHomeDir()
			altPath := filepath.Join(homeDir, "go", "bin", "gopls")
			if _, statErr := os.Stat(altPath); statErr == nil {
				cmdPath = altPath
				err = nil
			}
		}
	}
	if err != nil {
		return fmt.Errorf("server binary not found: %q; install with 'go install golang.org/x/tools/gopls@latest'", cmd)
	}

	c.goplsPath = cmdPath

	// Determine root path from working directory
	rootPath := "."
	if wd := os.Getenv("LLM_WORKDIR"); wd != "" {
		rootPath = wd
	} else if cwd, err := os.Getwd(); err == nil {
		rootPath = cwd
	}

	// Start the gopls LSP process
	lsp, err := startLSP(ctx, cmdPath, rootPath)
	if err != nil {
		return fmt.Errorf("failed to start gopls LSP: %w", err)
	}

	c.lsp = lsp
	c.active = true
	return nil
}

// startLSP launches a gopls process and performs the initialize handshake.
func startLSP(ctx context.Context, goplsPath, rootPath string) (*LSPClient, error) {
	cmd := exec.CommandContext(ctx, goplsPath, "-mode=stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// stderr is swallowed but useful for debugging; we can optionally log it
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start gopls: %w", err)
	}

	lsp := &LSPClient{
		cmd:      cmd,
		stdin:    stdin,
		reader:   bufio.NewReaderSize(stdout, 10*1024*1024),
		rootPath: rootPath,
	}

	// Perform initialize handshake
	initParams := map[string]interface{}{
		"processId":             nil,
		"rootUri":               filePathToURI(rootPath),
		"capabilities":          map[string]interface{}{},
		"initializationOptions": nil,
	}

	if err := lsp.sendRequest(ctx, "initialize", initParams); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("initialize request: %w", err)
	}
	initResult, err := lsp.readResponse(ctx)
	if err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("initialize response: %w", err)
	}
	_ = initResult // we don't need to parse capabilities right now

	// Send initialized notification
	if err := lsp.sendNotification("initialized", map[string]interface{}{}); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("initialized notification: %w", err)
	}

	return lsp, nil
}

// sendRequest sends a JSON-RPC request and increments the message ID.
func (l *LSPClient) sendRequest(ctx context.Context, method string, params interface{}) error {
	l.mu.Lock()
	l.msgID++
	id := l.msgID
	l.mu.Unlock()

	req := lspRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	return l.writeMessage(req)
}

// sendNotification sends a JSON-RPC notification (no ID).
func (l *LSPClient) sendNotification(method string, params interface{}) error {
	notif := lspNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return l.writeMessage(notif)
}

// writeMessage writes a JSON-RPC message with Content-Length header.
func (l *LSPClient) writeMessage(msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := l.stdin.Write([]byte(header)); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := l.stdin.Write(data); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return nil
}

// readResponse reads a JSON-RPC response, consuming Content-Length headers.
func (l *LSPClient) readResponse(ctx context.Context) (json.RawMessage, error) {
	return l.readMessage(ctx)
}

// readMessage reads one complete LSP message (header + body) from the buffered reader.
func (l *LSPClient) readMessage(ctx context.Context) (json.RawMessage, error) {
	contentLength := 0
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		line, err := l.reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			// End of headers
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			fmt.Sscanf(line, "Content-Length: %d", &contentLength)
		}
	}

	if contentLength <= 0 {
		return nil, fmt.Errorf("invalid Content-Length: %d", contentLength)
	}

	// Read exactly contentLength bytes
	body := make([]byte, contentLength)
	_, err := io.ReadFull(l.reader, body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return body, nil
}

// textDocumentDidOpen sends a didOpen notification to gopls.
func (l *LSPClient) textDocumentDidOpen(ctx context.Context, filePath string) error {
	uri := filePathToURI(filePath)
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        uri,
			"languageId": "go",
			"version":    1,
			"text":       string(content),
		},
	}
	return l.sendNotification("textDocument/didOpen", params)
}

// textDocumentDefinition calls textDocument/definition.
func (l *LSPClient) textDocumentDefinition(ctx context.Context, filePath string, line, character int) ([]lspLocation, error) {
	uri := filePathToURI(filePath)
	if err := l.textDocumentDidOpen(ctx, filePath); err != nil {
		return nil, err
	}

	params := lspTextDocumentPositionParams{
		TextDocument: lspTextDocumentIdentifier{URI: uri},
		Position:     lspPosition{Line: line, Character: character},
	}
	if err := l.sendRequest(ctx, "textDocument/definition", params); err != nil {
		return nil, err
	}
	raw, err := l.readResponse(ctx)
	if err != nil {
		return nil, err
	}

	var locations []lspLocation
	if err := json.Unmarshal(raw, &locations); err != nil {
		// Could be a single location
		var single lspLocation
		if err2 := json.Unmarshal(raw, &single); err2 == nil {
			locations = []lspLocation{single}
		} else {
			return nil, fmt.Errorf("parse definition result: %w", err)
		}
	}
	return locations, nil
}

// textDocumentReferences calls textDocument/references.
func (l *LSPClient) textDocumentReferences(ctx context.Context, filePath string, line, character int) ([]lspLocation, error) {
	uri := filePathToURI(filePath)
	if err := l.textDocumentDidOpen(ctx, filePath); err != nil {
		return nil, err
	}

	params := lspReferenceParams{
		TextDocument: lspTextDocumentIdentifier{URI: uri},
		Position:     lspPosition{Line: line, Character: character},
		Context:      lspReferenceContext{IncludeDeclaration: true},
	}
	if err := l.sendRequest(ctx, "textDocument/references", params); err != nil {
		return nil, err
	}
	raw, err := l.readResponse(ctx)
	if err != nil {
		return nil, err
	}

	var locations []lspLocation
	if err := json.Unmarshal(raw, &locations); err != nil {
		return nil, fmt.Errorf("parse references result: %w", err)
	}
	return locations, nil
}

// textDocumentHover calls textDocument/hover.
func (l *LSPClient) textDocumentHover(ctx context.Context, filePath string, line, character int) (*lspHover, error) {
	uri := filePathToURI(filePath)
	if err := l.textDocumentDidOpen(ctx, filePath); err != nil {
		return nil, err
	}

	params := lspTextDocumentPositionParams{
		TextDocument: lspTextDocumentIdentifier{URI: uri},
		Position:     lspPosition{Line: line, Character: character},
	}
	if err := l.sendRequest(ctx, "textDocument/hover", params); err != nil {
		return nil, err
	}
	raw, err := l.readResponse(ctx)
	if err != nil {
		return nil, err
	}

	var hover lspHover
	if err := json.Unmarshal(raw, &hover); err != nil {
		return nil, fmt.Errorf("parse hover result: %w", err)
	}
	return &hover, nil
}

// textDocumentRename calls textDocument/rename.
func (l *LSPClient) textDocumentRename(ctx context.Context, filePath string, line, character int, newName string) (*lspWorkspaceEdit, error) {
	uri := filePathToURI(filePath)
	if err := l.textDocumentDidOpen(ctx, filePath); err != nil {
		return nil, err
	}

	params := lspRenameParams{
		TextDocument: lspTextDocumentIdentifier{URI: uri},
		Position:     lspPosition{Line: line, Character: character},
		NewName:      newName,
	}
	if err := l.sendRequest(ctx, "textDocument/rename", params); err != nil {
		return nil, err
	}
	raw, err := l.readResponse(ctx)
	if err != nil {
		return nil, err
	}

	var edit lspWorkspaceEdit
	if err := json.Unmarshal(raw, &edit); err != nil {
		return nil, fmt.Errorf("parse rename result: %w", err)
	}
	return &edit, nil
}

// textDocumentDiagnostics gets diagnostics for a file.
func (l *LSPClient) textDocumentDiagnostics(ctx context.Context, filePath string) ([]lspDiagnostic, error) {
	if err := l.textDocumentDidOpen(ctx, filePath); err != nil {
		return nil, err
	}

	// gopls publishes diagnostics via textDocument/publishDiagnostics notification
	// which we receive async. We need to trigger a refresh.
	// The standard way is to call textDocument/diagnostic (3.18+) or
	// workspace/diagnostic. For gopls, we can use workspace/semanticTokens/refresh
	// but the simplest is to call textDocument/codeAction with empty context.
	// Actually, gopls sends diagnostics as a notification after didOpen.
	// We should read any pending notification after didOpen.

	// First, try to read any pending notification (publishDiagnostics)
	// by peeking with a short timeout. Since we're using blocking IO,
	// we'll need to set a deadline or use a goroutine.
	// For simplicity, we'll use a channel-based approach.

	type diagResult struct {
		params *lspPublishDiagnosticsParams
		err    error
	}

	diagCh := make(chan diagResult, 1)
	go func() {
		// Try to read the next message - it should be a publishDiagnostics notification
		raw, err := l.readMessage(ctx)
		if err != nil {
			diagCh <- diagResult{err: err}
			return
		}
		// Check if it's a notification (no "id" field) with method publishDiagnostics
		var notif struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(raw, &notif); err != nil {
			diagCh <- diagResult{err: fmt.Errorf("parse notification: %w", err)}
			return
		}
		if notif.Method != "textDocument/publishDiagnostics" {
			diagCh <- diagResult{err: fmt.Errorf("unexpected method: %s", notif.Method)}
			return
		}
		var params lspPublishDiagnosticsParams
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			diagCh <- diagResult{err: fmt.Errorf("parse diagnostics params: %w", err)}
			return
		}
		diagCh <- diagResult{params: &params}
	}()

	select {
	case result := <-diagCh:
		if result.err != nil {
			return nil, fmt.Errorf("read diagnostics: %w", result.err)
		}
		return result.params.Diagnostics, nil
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("timeout waiting for diagnostics")
	}
}

// Close shuts down the gopls process gracefully.
func (c *Client) Close() error {
	if c.lsp != nil && c.lsp.cmd != nil && c.lsp.cmd.Process != nil {
		// Send shutdown request
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_ = c.lsp.sendRequest(ctx, "shutdown", nil)
		_, _ = c.lsp.readResponse(ctx)
		_ = c.lsp.sendNotification("exit", nil)

		_ = c.lsp.stdin.Close()
		_ = c.lsp.cmd.Wait()
	}
	c.active = false
	c.lsp = nil
	return nil
}

// IsServerConnected checks if the server is active.
func (c *Client) IsServerConnected(serverName string) bool {
	return c.active && serverName == "go-lsp"
}

// AvailableServers returns connected server names.
func (c *Client) AvailableServers() []string {
	if c.active {
		return []string{"go-lsp"}
	}
	return []string{}
}

// ListTools returns the list of tools available from gopls.
func (c *Client) ListTools(ctx context.Context, serverName string) ([]MCPTool, error) {
	if serverName != "go-lsp" {
		return nil, fmt.Errorf("server %q not connected", serverName)
	}
	if !c.active {
		return nil, fmt.Errorf("gopls server not active")
	}

	// Tool definitions are static — they match the LSP methods we implement
	tools := []MCPTool{
		{
			Name:        "definition",
			Description: "Find the definition of a symbol at a given location in a Go file. Returns the file path and line number where the symbol is defined.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"file_path": map[string]interface{}{"type": "string", "description": "Absolute or workspace-relative path to the Go file"},
					"line":      map[string]interface{}{"type": "integer", "description": "Line number (0-indexed)"},
					"character": map[string]interface{}{"type": "integer", "description": "Column position (0-indexed)"},
				},
				"required": []string{"file_path", "line", "character"},
			},
		},
		{
			Name:        "references",
			Description: "Find all references to a symbol in the workspace. Returns a list of file locations where the symbol is used.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"file_path": map[string]interface{}{"type": "string", "description": "Absolute or workspace-relative path to the Go file"},
					"line":      map[string]interface{}{"type": "integer", "description": "Line number (0-indexed)"},
					"character": map[string]interface{}{"type": "integer", "description": "Column position (0-indexed)"},
				},
				"required": []string{"file_path", "line", "character"},
			},
		},
		{
			Name:        "hover",
			Description: "Get type information, documentation, and signature for a symbol. Returns type annotations and docstring.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"file_path": map[string]interface{}{"type": "string", "description": "Absolute or workspace-relative path to the Go file"},
					"line":      map[string]interface{}{"type": "integer", "description": "Line number (0-indexed)"},
					"character": map[string]interface{}{"type": "integer", "description": "Column position (0-indexed)"},
				},
				"required": []string{"file_path", "line", "character"},
			},
		},
		{
			Name:        "diagnostics",
			Description: "Run type checking and diagnostics on a Go file. Reports compilation errors, type errors, and lint warnings.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"file_path": map[string]interface{}{"type": "string", "description": "Path to the Go file to check"},
				},
				"required": []string{"file_path"},
			},
		},
		{
			Name:        "rename",
			Description: "Rename a symbol safely across the entire workspace. Handles all references and import updates automatically.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"file_path": map[string]interface{}{"type": "string", "description": "Absolute or workspace-relative path to the Go file"},
					"line":      map[string]interface{}{"type": "integer", "description": "Line number (0-indexed)"},
					"character": map[string]interface{}{"type": "integer", "description": "Column position (0-indexed)"},
					"new_name":  map[string]interface{}{"type": "string", "description": "New name for the symbol"},
				},
				"required": []string{"file_path", "line", "character", "new_name"},
			},
		},
	}

	return tools, nil
}

// CallTool executes a tool on the gopls LSP server via JSON-RPC.
func (c *Client) CallTool(ctx context.Context, serverName string, toolName string, args map[string]interface{}) (string, error) {
	if serverName != "go-lsp" {
		return "", fmt.Errorf("server %q not connected", serverName)
	}
	if !c.active || c.lsp == nil {
		return "", fmt.Errorf("gopls server not active")
	}

	// Extract common args
	filePath, _ := args["file_path"].(string)
	line, _ := args["line"].(float64)
	character, _ := args["character"].(float64)

	// Resolve relative paths to absolute
	if filePath != "" && !filepath.IsAbs(filePath) {
		root := c.lsp.rootPath
		filePath = filepath.Join(root, filePath)
	}

	switch toolName {
	case "definition":
		locations, err := c.lsp.textDocumentDefinition(ctx, filePath, int(line), int(character))
		if err != nil {
			return "", fmt.Errorf("definition: %w", err)
		}
		return formatLocations(locations), nil

	case "references":
		locations, err := c.lsp.textDocumentReferences(ctx, filePath, int(line), int(character))
		if err != nil {
			return "", fmt.Errorf("references: %w", err)
		}
		return formatLocations(locations), nil

	case "hover":
		hover, err := c.lsp.textDocumentHover(ctx, filePath, int(line), int(character))
		if err != nil {
			return "", fmt.Errorf("hover: %w", err)
		}
		return formatHover(hover), nil

	case "diagnostics":
		diagnostics, err := c.lsp.textDocumentDiagnostics(ctx, filePath)
		if err != nil {
			return "", fmt.Errorf("diagnostics: %w", err)
		}
		return formatDiagnostics(diagnostics), nil

	case "rename":
		newName, _ := args["new_name"].(string)
		if newName == "" {
			return "", fmt.Errorf("rename requires new_name argument")
		}
		edit, err := c.lsp.textDocumentRename(ctx, filePath, int(line), int(character), newName)
		if err != nil {
			return "", fmt.Errorf("rename: %w", err)
		}
		return formatWorkspaceEdit(edit), nil

	case "safe_edit":
		return "", fmt.Errorf("safe_edit: not implemented via gopls LSP — use the built-in edit_file tool instead")

	case "extract_function":
		return "", fmt.Errorf("extract_function: gopls does not support code extraction refactoring via LSP")

	case "import_analyzer":
		return "", fmt.Errorf("import_analyzer: use the built-in diagnostics tool for import analysis")

	default:
		return "", fmt.Errorf("unknown tool: %q", toolName)
	}
}

// formatLocations formats LSP locations into readable output.
func formatLocations(locations []lspLocation) string {
	if len(locations) == 0 {
		return "No results found."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d location(s):\n\n", len(locations))
	for i, loc := range locations {
		path := uriToFilePath(loc.URI)
		fmt.Fprintf(&sb, "%d. %s:%d:%d\n",
			i+1, path, loc.Range.Start.Line+1, loc.Range.Start.Character)
	}
	return sb.String()
}

// formatHover formats hover response.
func formatHover(hover *lspHover) string {
	if hover == nil || hover.Contents.Value == "" {
		return "No hover information available."
	}

	var sb strings.Builder
	sb.WriteString(hover.Contents.Value)
	if hover.Range != nil {
		fmt.Fprintf(&sb, "\n\nRange: %d:%d - %d:%d",
			hover.Range.Start.Line+1, hover.Range.Start.Character,
			hover.Range.End.Line+1, hover.Range.End.Character)
	}
	return sb.String()
}

// formatDiagnostics formats diagnostic results.
func formatDiagnostics(diagnostics []lspDiagnostic) string {
	if len(diagnostics) == 0 {
		return "✅ No issues found."
	}

	severityNames := map[int]string{
		1: "ERROR",
		2: "WARNING",
		3: "INFO",
		4: "HINT",
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d diagnostic(s):\n\n", len(diagnostics))
	for _, diag := range diagnostics {
		severity := severityNames[diag.Severity]
		if severity == "" {
			severity = "UNKNOWN"
		}
		fmt.Fprintf(&sb, "[%s] Line %d: %s\n",
			severity,
			diag.Range.Start.Line+1,
			diag.Message)
	}
	return sb.String()
}

// formatWorkspaceEdit formats rename results.
func formatWorkspaceEdit(edit *lspWorkspaceEdit) string {
	if edit == nil || len(edit.Changes) == 0 {
		return "No changes made."
	}

	var sb strings.Builder
	totalChanges := 0
	for uri, edits := range edit.Changes {
		path := uriToFilePath(uri)
		fmt.Fprintf(&sb, "File: %s (%d edit(s))\n", path, len(edits))
		for _, e := range edits {
			preview := e.NewText
			if len(preview) > 50 {
				preview = preview[:50] + "..."
			}
			fmt.Fprintf(&sb, "  Line %d: replace with %q\n",
				e.Range.Start.Line+1, preview)
			totalChanges++
		}
	}
	fmt.Fprintf(&sb, "\nTotal: %d change(s) across %d file(s)", totalChanges, len(edit.Changes))
	return sb.String()
}

// MCPTool represents a tool available from an external server.
type MCPTool struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
}

// DiscoverToolsByServerName starts a server and discovers its tools.
func DiscoverToolsByServerName(ctx context.Context, serverName string, cmdPath string, cmdArgs ...string) ([]MCPTool, error) {
	if serverName == "go-lsp" && cmdPath == "" {
		cmdPath = os.Getenv("MCP_GO_LSP_PATH")
		if cmdPath == "" {
			cmdPath = "gopls"
		}
	}

	c := NewClient(0)
	defer c.Close()

	if err := c.StartServer(ctx, serverName, cmdPath, cmdArgs...); err != nil {
		return nil, err
	}

	return c.ListTools(ctx, serverName)
}
