package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	deepseek "github.com/cohesion-org/deepseek-go"
	"github.com/promacanthus/code-editing-agent/pkg/tool"
)

// AdapterRegistry converts MCP tools to agent tool definitions.
type AdapterRegistry struct {
	mcpClient *Client
}

// NewAdapterRegistry creates a new adapter registry.
func NewAdapterRegistry(mcpClient *Client) *AdapterRegistry {
	return &AdapterRegistry{mcpClient: mcpClient}
}

// ConvertMCPToolsToDefinitions converts all tools from a named MCP server
// to tool.Definition instances that can be used by the agent.
func (ar *AdapterRegistry) ConvertMCPToolsToDefinitions(
	ctx context.Context,
	serverName string,
) ([]tool.Definition, error) {
	// List tools from the MCP server
	mcpTools, err := ar.mcpClient.ListTools(ctx, serverName)
	if err != nil {
		return nil, err
	}

	var definitions []tool.Definition
	for _, mcpTool := range mcpTools {
		// Create a tool definition for each MCP tool
		def := ar.ConvertSingleTool(mcpTool, serverName)
		definitions = append(definitions, def)
	}

	return definitions, nil
}

// ConvertSingleTool converts a single MCP tool to a tool.Definition.
func (ar *AdapterRegistry) ConvertSingleTool(mcpTool MCPTool, serverName string) tool.Definition {
	// Namespace tool name to avoid collisions: mcp_gopls_find_definition
	toolName := fmt.Sprintf("mcp_%s_%s", serverName, mcpTool.Name)

	return tool.Definition{
		Name:        toolName,
		Description: mcpTool.Description,
		InputSchema: convertMCPSchemaToDeepSeekSchema(mcpTool.InputSchema),
		Function: func(input json.RawMessage) (string, error) {
			// Execute the tool on the MCP server
			var args map[string]interface{}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("failed to unmarshal tool input: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), ar.mcpClient.timeout)
			defer cancel()

			result, err := ar.mcpClient.CallTool(ctx, serverName, mcpTool.Name, args)
			if err != nil {
				return "", err
			}
			return result, nil
		},
	}
}

// convertMCPSchemaToDeepSeekSchema converts an MCP schema (map[string]interface{})
// to a DeepSeek FunctionParameters schema.
func convertMCPSchemaToDeepSeekSchema(mcpSchema map[string]interface{}) *deepseek.FunctionParameters {
	if mcpSchema == nil {
		return &deepseek.FunctionParameters{
			Type:       "object",
			Properties: map[string]interface{}{},
			Required:   []string{},
		}
	}

	// Extract properties
	properties := map[string]interface{}{}
	if props, ok := mcpSchema["properties"].(map[string]interface{}); ok {
		properties = props
	}

	// Extract required fields
	required := []string{}
	if req, ok := mcpSchema["required"].([]interface{}); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				required = append(required, s)
			}
		}
	}

	return &deepseek.FunctionParameters{
		Type:       "object",
		Properties: properties,
		Required:   required,
	}
}

// MergeToolDefinitions combines built-in tools with MCP tools,
// handling name collisions by preferring MCP tools (prefixed with mcp_).
func MergeToolDefinitions(
	builtInTools []tool.Definition,
	mcpTools []tool.Definition,
) []tool.Definition {
	// Create a map of built-in tools
	builtInMap := make(map[string]tool.Definition)
	for _, t := range builtInTools {
		builtInMap[t.Name] = t
	}

	// Add MCP tools, overwriting any collisions
	// (MCP tools are prefixed with mcp_, so collisions are rare)
	merged := make([]tool.Definition, 0, len(builtInTools)+len(mcpTools))
	merged = append(merged, builtInTools...)
	merged = append(merged, mcpTools...)

	return merged
}
