package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	deepseek "github.com/cohesion-org/deepseek-go"
	"github.com/promacanthus/code-editing-agent/pkg/tool"
)

type Agent interface {
	Run(ctx context.Context) error
}

var _ Agent = (*agent)(nil)

// agent is the agent that runs the inference.
type agent struct {
	client          *deepseek.Client
	model           string
	assistantName   string
	getUserMessage  func() (string, bool)
	toolDefinitions []tool.Definition
	tools           []deepseek.Tool
}

// New creates a new agent.
func New(client *deepseek.Client, model string, assistantName string, fn func() (string, bool), toolDefinitions []tool.Definition) Agent {
	tools := make([]deepseek.Tool, 0, len(toolDefinitions))
	for _, t := range toolDefinitions {
		tool := deepseek.Tool{
			Type: "function",
			Function: deepseek.Function{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		}
		tools = append(tools, tool)
	}

	return &agent{
		client:          client,
		model:           model,
		assistantName:   assistantName,
		getUserMessage:  fn,
		toolDefinitions: toolDefinitions,
		tools:           tools,
	}
}

// Run starts the agent.
func (a *agent) Run(ctx context.Context) error {
	conversation := []deepseek.ChatCompletionMessage{
		{
			Role:    deepseek.ChatMessageRoleSystem,
			Content: "You are a senior software engineer assistant. Always respond in English. You have access to tools to read, edit, search, run shell commands, inspect git diffs, and review GitHub Pull Requests in the user's project directory.\n\n## File Reading Strategy (Critical for Large Projects)\nALWAYS use smart_read_file (not read_file) for reading files. smart_read_file offers 4 strategies:\n1. **summary=true** — Show file outline (function signatures, types, constants with line numbers). Use this FIRST when exploring a new file.\n2. **symbol=\"Name\"** — Extract only a specific function/type/method. Much faster for large files.\n3. **line_start=N&line_end=M** — Read exact line range.\n4. **No args** — Full file with auto-truncation at 500 lines. Avoid for files over 1000 lines.\n\n## Knowledge Base Search (Pinecone)\nUse `search_knowledge` when you need context, documentation, architecture details, or code patterns that are NOT available in the local project directory. Examples:\n- \"How does the crypto framework work?\"\n- \"What are the build steps for a beacon?\"\n- \"How does UTM validation work?\"\n- \"What is the architecture of this project?\"\n- \"Show me documentation about X\"\n`search_knowledge` performs semantic search against an external knowledge base — use it before guessing assumptions about framework internals.\n\n## Knowledge Base Ingestion (Pinecone)\nUse `pinecone_ingest` to populate or refresh the Pinecone knowledge base with the crypto-framework content. Run this after significant changes to docs, source code, or scripts so the knowledge search stays up to date. You can use `dry_run=true` first to preview what would be indexed without sending anything. Use `reset_index=true` to clear the index before re-ingesting (useful for a full refresh).\n\nThe tool automatically scans `.md`, `.go`, `.sh`, `.yaml`, `.yml`, `.json`, `.toml` files from the crypto-framework directory, chunks them, generates embeddings via NVIDIA API, and upserts them to Pinecone.\n\n## Framework Status\nUse `framework_status` to get a structured health report of the crypto-framework. It scans all source files in `internal/implant/modules/` and detects:\n- **✅ Functional** — real implementations\n- **❌ Not Implemented** — stubs, placeholders, files that print fake success messages\n- **⚠️ Partial** — files with TODOs or FIXMEs\n- **📝 Planned** — minimal or early-stage files\n\nThe tool writes `STATUS.md` to the framework root and returns a summary. Run it after making changes to see your progress. It respects `.gitignore` and skips vendor/module cache directories.\n\n## Module Scaffolding\nUse `scaffold_module` to create a new implant module with full directory structure:\n  scaffold_module --name clipboard_monitor --description \"Monitors clipboard for wallet addresses\"\n  scaffold_module --name api_harvester --platforms linux,darwin --dry_run=true\nCreates main implementation, platform stubs (windows/linux/darwin), test file, and updates registry.\n\n## Batch Fix Stubs\nUse `batch_fix_modules` to auto-fix all non-functional modules across the framework:\n  batch_fix_modules                                              # fix all ❌ stubs\n  batch_fix_modules --filter \"status:❌,module:crypto\"          # filter by status + module\n  batch_fix_modules --filter \"name:Injection\" --dry_run=true   # preview without writing\n  batch_fix_modules --fix_strategy aggressive                    # add constructors + imports\nScans STATUS.md, finds ❌/⚠️ modules, adds missing Run() methods, imports, and constructors.\n\n## Compile Error Fixer\nUse `fix_compile_errors` to break the edit-build-edit loop:\n  fix_compile_errors --package ./internal/implant/...\n  fix_compile_errors --package ./internal/implant/modules/process_injection --context_lines 5\nRuns go build, parses ALL errors, reads source context with line numbers, classifies error types.\n\n## Improvement Workflow\nWhen asked to improve or fix code:\n1. Use list_files or search_code to locate the relevant files.\n2. Use smart_read_file with summary=true to see the file structure.\n3. Use smart_read_file with symbol=\"...\" to extract relevant functions.\n4. Use git_diff to see any existing uncommitted changes.\n5. Use edit_file to apply the fix or improvement.\n6. Use fix_compile_errors or run_command (go build ./...) to verify the change compiles and tests pass.\n7. Summarize what you changed and why.\n\n## PR Discovery & Review Workflow\nWhen asked about open pull requests:\n1. Use github_list_prs with owner and repo to find open PRs.\n2. github_list_prs returns PR number, title, author, labels, draft status, and file count.\n3. Once you have a PR number, use github_pr to fetch the full diff and review.\n\nWhen asked to review a specific PR:\n1. Use github_pr --owner X --repo Y --number N to fetch the diff.\n2. For large PRs, use --files_only=true first to see what changed.\n3. Analyze the diff for: bugs, security issues, missing tests, performance problems, and style issues.\n4. Suggest concrete fixes. If the user says 'apply it', use edit_file to make the changes directly.\n\nBe concise and technical. Prefer action over explanation.",
		},
	}
	fmt.Printf("Chat with %s (use 'ctrl-c' to quit)\n", a.assistantName)

	readUserInput := true
	for {
		if readUserInput {
			fmt.Print("\u001b[94mYou\u001b[0m: ")
			userInput, ok := a.getUserMessage()
			if !ok {
				break
			}
			fmt.Println() // newline after user input

			userMessage := deepseek.ChatCompletionMessage{
				Role:    deepseek.ChatMessageRoleUser,
				Content: userInput,
			}
			conversation = append(conversation, userMessage)
		}

		// Trim conversation to stay within model context window before inference.
		// Budget is 60K chars (~15K tokens) to leave room for tool definitions + response.
		conversation = trimConversation(conversation, 60_000)

		// Always pass tools — avoids NVIDIA 502 on plain streaming calls
		message, err := a.runInference(ctx, conversation, true)
		if err != nil {
			return err
		}
		conversation = append(conversation, *message)

		const maxToolResultChars = 20_000
		toolResults := []deepseek.ChatCompletionMessage{}
		for _, toolCall := range message.ToolCalls {
			result := a.executeTool(toolCall.ID, toolCall.Function.Name, toolCall.Function.Arguments)
			if len(result.Content) > maxToolResultChars {
				result.Content = result.Content[:maxToolResultChars] + "\n[...output truncated, use more specific query to see more...]"
			}
			toolResults = append(toolResults, result)
		}

		if len(toolResults) == 0 {
			readUserInput = true
			// fmt.Printf("\u001b[92m%s\u001b[0m: %s\n", a.assistantName, message.Content) // Removed for streaming
			continue
		}
		readUserInput = false
		conversation = append(conversation, toolResults...)
	}
	return nil
}

// trimConversation removes old messages when the total content size exceeds maxChars,
// always preserving the system message at index 0 and the most recent messages.
// If a single remaining message is still too large it is truncated in-place.
func trimConversation(conversation []deepseek.ChatCompletionMessage, maxChars int) []deepseek.ChatCompletionMessage {
	const truncSuffix = "\n[...trimmed to fit context window...]"

	measure := func(msgs []deepseek.ChatCompletionMessage) int {
		n := 0
		for _, m := range msgs {
			n += len(m.Content)
			for _, tc := range m.ToolCalls {
				n += len(tc.Function.Arguments)
			}
		}
		return n
	}

	// Phase 1: drop oldest non-system messages.
	for len(conversation) > 2 && measure(conversation) > maxChars {
		conversation = append(conversation[:1], conversation[2:]...)
	}

	// Phase 2: if a single non-system message still exceeds the budget, truncate it.
	for i := 1; i < len(conversation) && measure(conversation) > maxChars; i++ {
		excess := measure(conversation) - maxChars
		if len(conversation[i].Content) > excess+len(truncSuffix) {
			conversation[i].Content = conversation[i].Content[:len(conversation[i].Content)-excess] + truncSuffix
		} else if len(conversation[i].Content) > len(truncSuffix) {
			conversation[i].Content = conversation[i].Content[:len(truncSuffix)] + truncSuffix
		}
	}

	return conversation
}

// runInference runs the inference and returns the message.
func (a *agent) runInference(ctx context.Context, conversation []deepseek.ChatCompletionMessage, includeTools bool) (*deepseek.ChatCompletionMessage, error) {
	if includeTools {
		request := &deepseek.StreamChatCompletionRequest{
			Model:     a.model,
			Messages:  conversation,
			Stream:    true,
			MaxTokens: 4096,
			Tools:     a.tools,
		}

		stream, err := a.client.CreateChatCompletionStream(ctx, request)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err := stream.Close(); err != nil {
				fmt.Printf("Warning: failed to close stream: %v\n", err)
			}
		}()

		var fullContent strings.Builder
		var toolCalls []deepseek.ToolCall
		var role string
		var thinkingStarted bool
		var thinkingContentPrinted bool

		fmt.Printf("\u001b[92m%s\u001b[0m: ", a.assistantName)

		var finishReason string
		for {
			resp, err := stream.Recv()
			if err != nil {
				if err.Error() == "EOF" {
					break
				}
				return nil, err
			}

			if len(resp.Choices) == 0 {
				continue
			}

			if resp.Choices[0].FinishReason != "" {
				finishReason = resp.Choices[0].FinishReason
			}

			delta := resp.Choices[0].Delta
			if delta.Role != "" {
				role = delta.Role
			}

			if delta.ReasoningContent != "" {
				if !thinkingStarted {
					fmt.Print("\n\u001b[90m[Thinking...]\u001b[0m\n")
					thinkingStarted = true
				}
				fmt.Print(delta.ReasoningContent)
				thinkingContentPrinted = true
			}

			if delta.Content != "" {
				if thinkingStarted {
					fmt.Print("\n\u001b[90m[Done Thinking]\u001b[0m\n")
					thinkingStarted = false
				}
				fmt.Print(delta.Content)
				fullContent.WriteString(delta.Content)
			}

			for _, tc := range delta.ToolCalls {
				if len(toolCalls) <= tc.Index {
					// Expand toolCalls slice if needed
					for len(toolCalls) <= tc.Index {
						toolCalls = append(toolCalls, deepseek.ToolCall{})
					}
					toolCalls[tc.Index].ID = tc.ID
					toolCalls[tc.Index].Type = tc.Type
					toolCalls[tc.Index].Function.Name = tc.Function.Name
					toolCalls[tc.Index].Function.Arguments = tc.Function.Arguments
				} else {
					if tc.ID != "" {
						toolCalls[tc.Index].ID = tc.ID
					}
					if tc.Function.Name != "" {
						toolCalls[tc.Index].Function.Name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						toolCalls[tc.Index].Function.Arguments += tc.Function.Arguments
					}
				}
			}
		}
		fmt.Println()

		// Print [Done Thinking] only if reasoning content was actually shown but never closed by content
		if thinkingStarted && thinkingContentPrinted {
			fmt.Print("\n\u001b[90m[Done Thinking]\u001b[0m\n")
		}

		if role == "" {
			role = deepseek.ChatMessageRoleAssistant
		}

		// Validate tool call arguments are complete JSON when finish_reason=length
		if finishReason == "length" {
			for i, tc := range toolCalls {
				if tc.Function.Arguments != "" && !json.Valid([]byte(tc.Function.Arguments)) {
					// Truncated JSON — discard this tool call to avoid confusing the model
					// The model will naturally retry on the next inference turn
					fmt.Printf("\n\u001b[93m[Warning]\u001b[0m %s tool call was truncated (finish_reason=length). Discarding incomplete arguments.\n", tc.Function.Name)
					toolCalls[i].Function.Arguments = ""
				}
			}
		}

		return &deepseek.ChatCompletionMessage{
			Role:      role,
			Content:   fullContent.String(),
			ToolCalls: toolCalls,
		}, nil
	}

	// ALWAYS use streaming to avoid deadlocks in body reading
	request := &deepseek.StreamChatCompletionRequest{
		Model:    a.model,
		Messages: conversation,
		Stream:   true,
	}

	stream, err := a.client.CreateChatCompletionStream(ctx, request)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := stream.Close(); err != nil {
			fmt.Printf("Warning: Failed to close stream: %v\n", err)
		}
	}()

	var fullContent strings.Builder
	var role string
	var thinkingStarted bool
	var thinkingContentPrinted bool

	fmt.Printf("\u001b[92m%s\u001b[0m: ", a.assistantName)

	for {
		resp, err := stream.Recv()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, err
		}

		if len(resp.Choices) == 0 {
			continue
		}

		delta := resp.Choices[0].Delta
		if delta.Role != "" {
			role = delta.Role
		}

		if delta.ReasoningContent != "" {
			if !thinkingStarted {
				fmt.Print("\n\u001b[90m[Thinking...]\u001b[0m\n")
				thinkingStarted = true
			}
			fmt.Print(delta.ReasoningContent)
			thinkingContentPrinted = true
		}

		if delta.Content != "" {
			if thinkingStarted {
				fmt.Print("\n\u001b[90m[Done Thinking]\u001b[0m\n")
				thinkingStarted = false
			}
			fmt.Print(delta.Content)
			fullContent.WriteString(delta.Content)
		}
	}
	fmt.Println()

	// Print [Done Thinking] only if reasoning content was actually shown but never closed by content
	if thinkingStarted && thinkingContentPrinted {
		fmt.Print("\n\u001b[90m[Done Thinking]\u001b[0m\n")
	}

	if role == "" {
		role = deepseek.ChatMessageRoleAssistant
	}

	return &deepseek.ChatCompletionMessage{
		Role:    role,
		Content: fullContent.String(),
	}, nil
}

// executeTool executes the tool and returns the result.
func (a *agent) executeTool(id, name, args string) deepseek.ChatCompletionMessage {
	var toolDef tool.Definition
	var found bool
	for _, t := range a.toolDefinitions {
		if t.Name == name {
			toolDef = t
			found = true
			break
		}
	}
	if !found {
		return deepseek.ChatCompletionMessage{
			Role:       deepseek.ChatMessageRoleTool,
			Content:    fmt.Sprintf("tool not found: %s", name),
			ToolCallID: id,
		}
	}

	fmt.Printf("\u001b[92mtool\u001b[0m: %s (%s)\n", name, args)
	response, err := toolDef.Function(json.RawMessage(args))
	if err != nil {
		// If smart_read_file fails with JSON parse error, try graceful degradation
		if name == "smart_read_file" && strings.Contains(err.Error(), "invalid input") {
			var partial struct {
				Path         string `json:"path"`
				LineStart    int    `json:"line_start"`
				LineEnd      int    `json:"line_end"`
				Symbol       string `json:"symbol"`
				Summary      bool   `json:"summary"`
				MaxLines     int    `json:"max_lines"`
				ContextLines int    `json:"context_lines"`
			}
			if unmarshalErr := json.Unmarshal(json.RawMessage(args), &partial); unmarshalErr == nil && partial.Path != "" {
				// Partial JSON was parseable enough — build a minimal valid call
				minimal, _ := json.Marshal(struct {
					Path         string `json:"path"`
					LineStart    int    `json:"line_start,omitempty"`
					LineEnd      int    `json:"line_end,omitempty"`
					Symbol       string `json:"symbol,omitempty"`
					Summary      bool   `json:"summary,omitempty"`
					MaxLines     int    `json:"max_lines,omitempty"`
					ContextLines int    `json:"context_lines,omitempty"`
				}{
					Path:         partial.Path,
					LineStart:    partial.LineStart,
					LineEnd:      partial.LineEnd,
					Symbol:       partial.Symbol,
					Summary:      partial.Summary,
					MaxLines:     partial.MaxLines,
					ContextLines: partial.ContextLines,
				})
				fmt.Printf("\u001b[93m[Fallback]\u001b[0m Retrying smart_read_file with reconstructed arguments\n")
				response, fallbackErr := toolDef.Function(json.RawMessage(minimal))
				if fallbackErr == nil {
					return deepseek.ChatCompletionMessage{
						Role:       deepseek.ChatMessageRoleTool,
						Content:    response,
						ToolCallID: id,
					}
				}
			}
		}

		return deepseek.ChatCompletionMessage{
			Role:       deepseek.ChatMessageRoleTool,
			Content:    err.Error(),
			ToolCallID: id,
		}
	}
	return deepseek.ChatCompletionMessage{
		Role:       deepseek.ChatMessageRoleTool,
		Content:    response,
		ToolCallID: id,
	}
}
