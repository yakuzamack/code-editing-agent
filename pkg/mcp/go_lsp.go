package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/promacanthus/code-editing-agent/pkg/tool"
)

// GoLSPDefinitions returns tool definitions for Go language server operations.
// These tools provide semantic code analysis via gopls.
func GoLSPDefinitions(mcpClient *Client) []tool.Definition {
	return []tool.Definition{
		createFindDefinitionTool(mcpClient),
		createFindReferencesTool(mcpClient),
		createHoverTool(mcpClient),
		createDiagnosticsTool(mcpClient),
		createRenameTool(mcpClient),
	}
}

// FindDefinitionInput is the input for the find_definition tool.
type FindDefinitionInput struct {
	FilePath   string `json:"file_path" jsonschema:"description=Absolute or workspace-relative path to the Go file"`
	Line       int    `json:"line" jsonschema:"description=Line number (0-indexed)"`
	Character  int    `json:"character" jsonschema:"description=Column position (0-indexed)"`
}

func createFindDefinitionTool(mcpClient *Client) tool.Definition {
	return tool.Definition{
		Name:        "mcp_gopls_find_definition",
		Description: "Find the definition of a symbol at a given location in a Go file. Returns the file path and line number where the symbol is defined. Useful for understanding where a function, type, or variable is declared.",
		InputSchema: tool.GenerateSchema[FindDefinitionInput](),
		Function: func(input json.RawMessage) (string, error) {
			var args FindDefinitionInput
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			// Call gopls definition endpoint
			ctx, cancel := context.WithTimeout(context.Background(), mcpClient.timeout)
			defer cancel()

			callArgs := map[string]interface{}{
				"file_path":  args.FilePath,
				"line":       args.Line,
				"character":  args.Character,
			}

			return mcpClient.CallTool(ctx, "go-lsp", "definition", callArgs)
		},
	}
}

// FindReferencesInput is the input for the find_references tool.
type FindReferencesInput struct {
	FilePath   string `json:"file_path" jsonschema:"description=Absolute or workspace-relative path to the Go file"`
	Line       int    `json:"line" jsonschema:"description=Line number (0-indexed)"`
	Character  int    `json:"character" jsonschema:"description=Column position (0-indexed)"`
}

func createFindReferencesTool(mcpClient *Client) tool.Definition {
	return tool.Definition{
		Name:        "mcp_gopls_find_references",
		Description: "Find all references to a symbol in the workspace. Returns a list of file locations where the symbol is used. Useful for impact analysis before refactoring.",
		InputSchema: tool.GenerateSchema[FindReferencesInput](),
		Function: func(input json.RawMessage) (string, error) {
			var args FindReferencesInput
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), mcpClient.timeout)
			defer cancel()

			callArgs := map[string]interface{}{
				"file_path":  args.FilePath,
				"line":       args.Line,
				"character":  args.Character,
			}

			return mcpClient.CallTool(ctx, "go-lsp", "references", callArgs)
		},
	}
}

// HoverInput is the input for the hover tool.
type HoverInput struct {
	FilePath   string `json:"file_path" jsonschema:"description=Absolute or workspace-relative path to the Go file"`
	Line       int    `json:"line" jsonschema:"description=Line number (0-indexed)"`
	Character  int    `json:"character" jsonschema:"description=Column position (0-indexed)"`
}

func createHoverTool(mcpClient *Client) tool.Definition {
	return tool.Definition{
		Name:        "mcp_gopls_hover",
		Description: "Get type information, documentation, and signature for a symbol at a given location. Returns type annotations, docstring, and other metadata. Useful for understanding function signatures and type information.",
		InputSchema: tool.GenerateSchema[HoverInput](),
		Function: func(input json.RawMessage) (string, error) {
			var args HoverInput
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), mcpClient.timeout)
			defer cancel()

			callArgs := map[string]interface{}{
				"file_path":  args.FilePath,
				"line":       args.Line,
				"character":  args.Character,
			}

			return mcpClient.CallTool(ctx, "go-lsp", "hover", callArgs)
		},
	}
}

// DiagnosticsInput is the input for the diagnostics tool.
type DiagnosticsInput struct {
	FilePath string `json:"file_path" jsonschema:"description=Optional file path to check. If empty, checks all files in the workspace."`
}

func createDiagnosticsTool(mcpClient *Client) tool.Definition {
	return tool.Definition{
		Name:        "mcp_gopls_diagnostics",
		Description: "Run type checking, linting, and diagnostics on Go code. Reports compilation errors, lint warnings, and code quality issues. Useful for validating changes compile correctly.",
		InputSchema: tool.GenerateSchema[DiagnosticsInput](),
		Function: func(input json.RawMessage) (string, error) {
			var args DiagnosticsInput
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), mcpClient.timeout)
			defer cancel()

			callArgs := map[string]interface{}{
				"file_path": args.FilePath,
			}

			return mcpClient.CallTool(ctx, "go-lsp", "diagnostics", callArgs)
		},
	}
}

// RenameInput is the input for the rename tool.
type RenameInput struct {
	FilePath   string `json:"file_path" jsonschema:"description=Absolute or workspace-relative path to the Go file"`
	Line       int    `json:"line" jsonschema:"description=Line number (0-indexed)"`
	Character  int    `json:"character" jsonschema:"description=Column position (0-indexed)"`
	NewName    string `json:"new_name" jsonschema:"description=New name for the symbol"`
}

func createRenameTool(mcpClient *Client) tool.Definition {
	return tool.Definition{
		Name:        "mcp_gopls_rename",
		Description: "Rename a symbol safely across the entire workspace. Handles all references and import updates automatically. Returns the list of affected files. SAFE: only renames valid symbol references, not strings or comments.",
		InputSchema: tool.GenerateSchema[RenameInput](),
		Function: func(input json.RawMessage) (string, error) {
			var args RenameInput
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), mcpClient.timeout)
			defer cancel()

			callArgs := map[string]interface{}{
				"file_path":  args.FilePath,
				"line":       args.Line,
				"character":  args.Character,
				"new_name":   args.NewName,
			}

			return mcpClient.CallTool(ctx, "go-lsp", "rename", callArgs)
		},
	}
}
