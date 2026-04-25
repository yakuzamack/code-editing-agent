package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/promacanthus/code-editing-agent/pkg/tool"
)

// RefactoringDefinitions returns tool definitions for advanced refactoring operations.
// These tools coordinate multi-file edits through LSP for safe code transformations.
func RefactoringDefinitions(mcpClient *Client) []tool.Definition {
	return []tool.Definition{
		createSafeEditTool(mcpClient),
		createExtractFunctionTool(mcpClient),
		createImportAnalyzerTool(mcpClient),
	}
}

// SafeEditInput is the input for the safe_edit tool.
type SafeEditInput struct {
	Description string `json:"description" jsonschema:"description=Human-readable description of what this edit does"`
	FilePath    string `json:"file_path" jsonschema:"description=Primary file to edit"`
	StartLine   int    `json:"start_line" jsonschema:"description=Starting line of the region (0-indexed, inclusive)"`
	EndLine     int    `json:"end_line" jsonschema:"description=Ending line of the region (0-indexed, inclusive)"`
	Replacement string `json:"replacement" jsonschema:"description=New code to replace the selection with"`
	DryRun      bool   `json:"dry_run" jsonschema:"description=If true, show affected files without applying changes"`
}

func createSafeEditTool(mcpClient *Client) tool.Definition {
	return tool.Definition{
		Name:        "mcp_gopls_safe_edit",
		Description: "Apply a code edit and automatically handle all downstream effects (imports, formatting, multi-file updates). First runs diagnostics to ensure the edit doesn't break the build. Returns list of modified files. Use DryRun=true to preview changes before applying.",
		InputSchema: tool.GenerateSchema[SafeEditInput](),
		Function: func(input json.RawMessage) (string, error) {
			var args SafeEditInput
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), mcpClient.timeout)
			defer cancel()

			callArgs := map[string]interface{}{
				"description": args.Description,
				"file_path":   args.FilePath,
				"start_line":  args.StartLine,
				"end_line":    args.EndLine,
				"replacement": args.Replacement,
				"dry_run":     args.DryRun,
			}

			return mcpClient.CallTool(ctx, "go-lsp", "safe_edit", callArgs)
		},
	}
}

// ExtractFunctionInput is the input for the extract_function tool.
type ExtractFunctionInput struct {
	FilePath     string `json:"file_path" jsonschema:"description=Path to the Go file"`
	StartLine    int    `json:"start_line" jsonschema:"description=Starting line of code to extract (0-indexed)"`
	EndLine      int    `json:"end_line" jsonschema:"description=Ending line of code to extract (0-indexed)"`
	FunctionName string `json:"function_name" jsonschema:"description=Name for the new function"`
}

func createExtractFunctionTool(mcpClient *Client) tool.Definition {
	return tool.Definition{
		Name:        "mcp_gopls_extract_function",
		Description: "Extract a block of code into a new function. Automatically handles parameter inference, return types, and calling the new function from the original location. Useful for reducing duplication and improving code organization.",
		InputSchema: tool.GenerateSchema[ExtractFunctionInput](),
		Function: func(input json.RawMessage) (string, error) {
			var args ExtractFunctionInput
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), mcpClient.timeout)
			defer cancel()

			callArgs := map[string]interface{}{
				"file_path":     args.FilePath,
				"start_line":    args.StartLine,
				"end_line":      args.EndLine,
				"function_name": args.FunctionName,
			}

			return mcpClient.CallTool(ctx, "go-lsp", "extract_function", callArgs)
		},
	}
}

// ImportAnalyzerInput is the input for the import_analyzer tool.
type ImportAnalyzerInput struct {
	FilePath string `json:"file_path" jsonschema:"description=Path to the Go file to analyze"`
}

func createImportAnalyzerTool(mcpClient *Client) tool.Definition {
	return tool.Definition{
		Name:        "mcp_gopls_import_analyzer",
		Description: "Analyze and optimize imports in a Go file. Finds unused imports, missing imports, and suggests reorganization. Useful for cleaning up dependencies and understanding module relationships.",
		InputSchema: tool.GenerateSchema[ImportAnalyzerInput](),
		Function: func(input json.RawMessage) (string, error) {
			var args ImportAnalyzerInput
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), mcpClient.timeout)
			defer cancel()

			callArgs := map[string]interface{}{
				"file_path": args.FilePath,
			}

			return mcpClient.CallTool(ctx, "go-lsp", "import_analyzer", callArgs)
		},
	}
}
