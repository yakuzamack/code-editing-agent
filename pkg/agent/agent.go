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
			Content: "You are a senior software engineer assistant. Always respond in English. You have access to tools to read, edit, search, run shell commands, inspect git diffs, and review GitHub Pull Requests in the user's project directory.\n\n## Improvement Workflow\nWhen asked to improve or fix code:\n1. Use list_files or search_code to locate the relevant files.\n2. Use read_file to understand the current implementation.\n3. Use git_diff to see any existing uncommitted changes.\n4. Use edit_file to apply the fix or improvement.\n5. Use crypto_test or run_command (go build ./...) to verify the change compiles and tests pass.\n6. Summarize what you changed and why.\n\n## PR Review Workflow\nWhen asked to review a GitHub PR:\n1. Use github_pr with the owner, repo, and PR number to fetch the diff.\n2. Analyze the diff for: bugs, security issues, missing tests, performance problems, and style issues.\n3. Suggest concrete fixes. If the user says 'apply it', use edit_file to make the changes directly.\n\nBe concise and technical. Prefer action over explanation.",
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

		// Always pass tools — avoids NVIDIA 502 on plain streaming calls
		message, err := a.runInference(ctx, conversation, true)
		if err != nil {
			return err
		}
		conversation = append(conversation, *message)

		toolResults := []deepseek.ChatCompletionMessage{}
		for _, toolCall := range message.ToolCalls {
			result := a.executeTool(toolCall.ID, toolCall.Function.Name, toolCall.Function.Arguments)
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

// runInference runs the inference and returns the message.
func (a *agent) runInference(ctx context.Context, conversation []deepseek.ChatCompletionMessage, includeTools bool) (*deepseek.ChatCompletionMessage, error) {
	if includeTools {
		request := &deepseek.StreamChatCompletionRequest{
			Model:    a.model,
			Messages: conversation,
			Stream:   true,
			Tools:    a.tools,
		}

		stream, err := a.client.CreateChatCompletionStream(ctx, request)
		if err != nil {
			return nil, err
		}
		defer stream.Close()

		var fullContent strings.Builder
		var toolCalls []deepseek.ToolCall
		var role string
		var thinkingStarted bool

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

		if role == "" {
			role = deepseek.ChatMessageRoleAssistant
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
	defer stream.Close()

	var fullContent strings.Builder
	var role string
	var thinkingStarted bool

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

	if role == "" {
		role = deepseek.ChatMessageRoleAssistant
	}

	return &deepseek.ChatCompletionMessage{
		Role:    role,
		Content: fullContent.String(),
	}, nil
}

func shouldUseTools(input string) bool {
	input = strings.ToLower(input)

	keywords := []string{
		"file",
		"files",
		"folder",
		"directory",
		"dir",
		"read",
		"open",
		"list",
		"show",
		"search",
		"grep",
		"edit",
		"write",
		"replace",
		"change",
		"update",
		"create",
		"delete",
		"rename",
		"project",
		"repo",
		"repository",
		"framework",
		"code",
		"bug",
		"fix",
		"refactor",
		"run",
		"command",
		"shell",
		"test",
		"build",
		"function",
		"package",
		"import",
		"path",
		"main.go",
		".go",
		"knowledge",
		"pinecone",
		"base",
		"pattern",
		"context",		"diff",
		"pr",
		"pull request",
		"pull-request",
		"review",
		"git",
		"commit",
		"branch",
		"improve",
		"fix",
		"refactor",
		"issue",
		"bug",	}

	for _, keyword := range keywords {
		if strings.Contains(input, keyword) {
			return true
		}
	}

	return false
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
