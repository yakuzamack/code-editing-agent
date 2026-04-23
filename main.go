package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	deepseek "github.com/cohesion-org/deepseek-go"
	"github.com/joho/godotenv"
	"github.com/promacanthus/code-editing-agent/pkg/agent"
	"github.com/promacanthus/code-editing-agent/pkg/mcp"
	"github.com/promacanthus/code-editing-agent/pkg/tool"
)

func run() {
	_ = godotenv.Load()

	providerFlag := flag.String("provider", envOrDefault("LLM_PROVIDER", ""), "Provider profile to use: deepseek or nvidia")
	workDir := flag.String("workdir", envOrDefault("LLM_WORKDIR", ""), "Path to the project directory the agent should operate on")
	baseURL := flag.String("base-url", envOrDefault("LLM_BASE_URL", ""), "Base URL for the chat-completions API")
	model := flag.String("model", envOrDefault("LLM_MODEL", ""), "Model name to use for chat completions")
	assistantName := flag.String("assistant-name", envOrDefault("LLM_ASSISTANT_NAME", ""), "Display name shown in the chat prompt")
	mcpServersFlag := flag.String("mcp-servers", envOrDefault("MCP_SERVERS", ""), "Comma-separated list of MCP servers to enable (e.g., go-lsp)")
	mcpTimeoutFlag := flag.String("mcp-timeout", envOrDefault("MCP_TIMEOUT", "30s"), "Timeout for MCP server operations (e.g., 30s, 2m)")
	flag.Parse()

	provider := normalizeProvider(*providerFlag)
	if provider == "" {
		provider = "deepseek"
	}

	effectiveBaseURL := firstNonEmpty(*baseURL, providerEnv(provider, "BASE_URL"), defaultBaseURL(provider))
	effectiveModel := firstNonEmpty(*model, providerEnv(provider, "MODEL"), defaultModel(provider))
	effectiveAssistantName := firstNonEmpty(*assistantName, providerEnv(provider, "ASSISTANT_NAME"), defaultAssistantName(provider))

	if *workDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Printf("Error: %s\n", err)
			return
		}
		*workDir = cwd
	}

	err := tool.SetWorkingDir(*workDir)
	if err != nil {
		fmt.Printf("Error: %s\n", err)
		return
	}

	apiKey := firstNonEmpty(os.Getenv("LLM_API_KEY"), providerAPIKey(provider))
	if apiKey == "" {
		fmt.Printf("Error: set a valid API key for provider %q\n", provider)
		return
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Minute, // Increased timeout for slow reasoning models
	}

	client, err := deepseek.NewClientWithOptions(
		apiKey,
		deepseek.WithBaseURL(normalizeBaseURL(effectiveBaseURL)),
		deepseek.WithHTTPClient(httpClient),
	)
	if err != nil {
		fmt.Printf("Error: %s\n", err)
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	getUserMessage := func() (string, bool) {
		if !scanner.Scan() {
			return "", false
		}
		return scanner.Text(), true
	}

	fmt.Printf("Using provider: %s\n", provider)
	fmt.Printf("Using working directory: %s\n", tool.WorkingDir())
	fmt.Printf("Using model: %s\n", effectiveModel)

	// Initialize MCP infrastructure
	allTools := tool.Definitions
	var mcpClient *mcp.Client
	if *mcpServersFlag != "" {
		mcpTimeout, err := time.ParseDuration(*mcpTimeoutFlag)
		if err != nil {
			fmt.Printf("Warning: invalid MCP_TIMEOUT %q, using default 30s\n", *mcpTimeoutFlag)
			mcpTimeout = 30 * time.Second
		}

		mcpClient = mcp.NewClient(mcpTimeout)
		ctx := context.Background()

		// Parse and enable requested MCP servers
		serverNames := strings.Split(*mcpServersFlag, ",")
		var mcpTools []tool.Definition

		for _, serverName := range serverNames {
			serverName = strings.TrimSpace(serverName)
			if serverName == "" {
				continue
			}

			switch serverName {
			case "go-lsp":
				// Try to start gopls
				if err := mcpClient.StartServer(ctx, "go-lsp", "gopls"); err != nil {
					fmt.Printf("Warning: failed to start gopls MCP server: %v\n", err)
					fmt.Println("  Install gopls with: go install golang.org/x/tools/gopls@latest")
					continue
				}

				// Also register gopls-specific typed tools (go_lsp.go, refactoring.go)
				goplsSpecific := mcp.GoLSPDefinitions(mcpClient)
				mcpTools = append(mcpTools, goplsSpecific...)

				// Discover tools from gopls via ListTools
				if goLspTools, err := mcpClient.ListTools(ctx, "go-lsp"); err != nil {
					fmt.Printf("Warning: failed to discover gopls tools: %v\n", err)
				} else {
					adapter := mcp.NewAdapterRegistry(mcpClient)
					for _, mcpTool := range goLspTools {
						def := adapter.ConvertSingleTool(mcpTool, "gopls")
						mcpTools = append(mcpTools, def)
					}
					fmt.Printf("Loaded %d tools from gopls\n", len(goLspTools))
				}

			default:
				fmt.Printf("Warning: unknown MCP server %q\n", serverName)
			}
		}

		// Merge MCP tools with built-in tools
		if len(mcpTools) > 0 {
			allTools = mcp.MergeToolDefinitions(tool.Definitions, mcpTools)
			fmt.Printf("Total tools available: %d (built-in) + %d (MCP) = %d\n",
				len(tool.Definitions), len(mcpTools), len(allTools))
		}
	}

	agent := agent.New(client, effectiveModel, effectiveAssistantName, getUserMessage, allTools)
	err = agent.Run(context.Background())
	if err != nil {
		fmt.Printf("Error: %s\n", err)
	}

	// Clean up MCP client after agent exits
	if mcpClient != nil {
		if err := mcpClient.Close(); err != nil {
			fmt.Printf("Warning: failed to close MCP client: %v\n", err)
		}
	}
}

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func normalizeBaseURL(baseURL string) string {
	if strings.HasSuffix(baseURL, "/") {
		return baseURL
	}
	return baseURL + "/"
}

func normalizeProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "", "deepseek":
		return provider
	case "nvidia", "openai", "integrate":
		return "nvidia"
	default:
		return provider
	}
}

func providerEnv(provider, suffix string) string {
	key := strings.ToUpper(provider) + "_" + suffix
	return os.Getenv(key)
}

func providerAPIKey(provider string) string {
	switch provider {
	case "deepseek":
		return os.Getenv("DEEPSEEK_API_KEY")
	case "nvidia":
		return firstNonEmpty(os.Getenv("NVIDIA_API_KEY"), os.Getenv("OPENAI_API_KEY"))
	default:
		return firstNonEmpty(os.Getenv("OPENAI_API_KEY"), os.Getenv("NVIDIA_API_KEY"), os.Getenv("DEEPSEEK_API_KEY"))
	}
}

func defaultBaseURL(provider string) string {
	switch provider {
	case "nvidia":
		return "https://integrate.api.nvidia.com/v1/"
	case "deepseek", "":
		return "https://api.deepseek.com/"
	default:
		return "https://api.deepseek.com/"
	}
}

func defaultModel(provider string) string {
	switch provider {
	case "nvidia":
		return "deepseek-ai/deepseek-v3.2"
	case "deepseek", "":
		return deepseek.DeepSeekChat
	default:
		return deepseek.DeepSeekChat
	}
}

func defaultAssistantName(provider string) string {
	switch provider {
	case "nvidia":
		return "NVIDIA"
	case "deepseek", "":
		return "DeepSeek"
	default:
		return "Assistant"
	}
}

func main() {
	// Check if the first argument is a tool name
	if len(os.Args) > 1 {
		toolName := os.Args[1]
		if handleToolExecution(toolName) {
			return
		}
	}

	// Default to running the interactive agent
	run()
}

func handleToolExecution(toolName string) bool {
	switch toolName {
	case "utm_validate":
		return executeUTMValidate()
	case "copilot-pr":
		return executeCopilotPR()
	default:
		return false
	}
}

func executeUTMValidate() bool {
	// Parse utm_validate specific flags - skip the "utm_validate" argument
	args := os.Args[2:] // Skip program name and "utm_validate"

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	phase := flag.String("phase", "", "Phase to execute: preflight, start_vm, build_beacon, deploy_beacon, wait_sessions, run_audit, check_sessions, full")
	vmName := flag.String("vm_name", "", "UTM VM name")
	shareDir := flag.String("share_dir", "", "UTM share directory path")
	apiSecret := flag.String("api_secret", "", "API secret for authentication")
	skipPreview := flag.Bool("skip_preview", false, "Skip preview and run directly")

	if err := flag.CommandLine.Parse(args); err != nil {
		fmt.Printf("Error parsing flags: %v\n", err)
		return false
	}

	if *phase == "" {
		fmt.Println("Error: --phase is required")
		fmt.Println("Usage: utm_validate --phase <phase> [--vm_name <name>] [--share_dir <path>] [--api_secret <secret>] [--skip_preview]")
		return true
	}

	// Create input for the utm_validate tool
	input := tool.UTMValidateInput{
		Phase:       *phase,
		VMName:      *vmName,
		ShareDir:    *shareDir,
		ApiSecret:   *apiSecret,
		SkipPreview: *skipPreview,
	}

	// Set working directory if needed
	if wd := os.Getenv("LLM_WORKDIR"); wd != "" {
		err := tool.SetWorkingDir(wd)
		if err != nil {
			fmt.Printf("Error setting working directory: %s\n", err)
			return true
		}
	}

	// Marshal input to JSON for the tool function
	inputJSON, err := json.Marshal(input)
	if err != nil {
		fmt.Printf("Error marshaling input: %s\n", err)
		return true
	}

	// Execute the utm_validate tool
	result, err := tool.UTMValidate(inputJSON)
	if err != nil {
		fmt.Printf("Error executing utm_validate: %s\n", err)
		return true
	}

	fmt.Println(result)
	return true
}

// executeCopilotPR runs the copilot-pr review subcommand.
// Usage: copilot-agent copilot-pr --owner <owner> --repo <repo> --number <pr-number> --token <github-token> [--output review.md]
func executeCopilotPR() bool {
	// Parse copilot-pr specific flags
	args := os.Args[2:] // Skip program name and "copilot-pr"

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	owner := flag.String("owner", "", "GitHub repository owner")
	repo := flag.String("repo", "", "GitHub repository name")
	number := flag.Int("number", 0, "Pull request number")
	token := flag.String("token", "", "GitHub token for API access")
	output := flag.String("output", "", "Optional file path to write the review output to")

	if err := flag.CommandLine.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		return false
	}

	// Resolve token: flag > env > GITHUB_TOKEN secret
	effectiveToken := firstNonEmpty(*token, os.Getenv("GITHUB_TOKEN"))
	if effectiveToken == "" {
		fmt.Fprintf(os.Stderr, "Error: GITHUB_TOKEN is required. Pass --token or set GITHUB_TOKEN env var.\n")
		return false
	}

	// Resolve owner/repo from flags or env
	effectiveOwner := firstNonEmpty(*owner, os.Getenv("GITHUB_REPOSITORY_OWNER"))
	effectiveRepo := firstNonEmpty(*repo, os.Getenv("GITHUB_REPOSITORY_NAME"))
	if effectiveOwner == "" || effectiveRepo == "" {
		// Try parsing GITHUB_REPOSITORY (owner/repo format)
		if fullRepo := os.Getenv("GITHUB_REPOSITORY"); fullRepo != "" {
			parts := strings.SplitN(fullRepo, "/", 2)
			if len(parts) == 2 {
				effectiveOwner = parts[0]
				effectiveRepo = parts[1]
			}
		}
	}

	if effectiveOwner == "" || effectiveRepo == "" || *number == 0 {
		fmt.Fprintf(os.Stderr, "Error: --owner, --repo, and --number are required (or set via GITHUB_REPOSITORY env).\n")
		return false
	}

	// Set GITHUB_TOKEN so the github_pr tool can use it
	os.Setenv("GITHUB_TOKEN", effectiveToken)

	// Set working directory to the repository root
	cwd, err := os.Getwd()
	if err == nil {
		_ = tool.SetWorkingDir(cwd)
	}

	fmt.Fprintf(os.Stderr, "🔍 Reviewing PR #%d: %s/%s\n", *number, effectiveOwner, effectiveRepo)

	// Step 1: Fetch PR metadata and file list (FilesOnly)
	fileListInput := tool.GithubPRInput{
		Owner:     effectiveOwner,
		Repo:      effectiveRepo,
		Number:    *number,
		FilesOnly: true,
	}
	fileListJSON, _ := json.Marshal(fileListInput)
	fileListResult, err := tool.ExecuteGithubPR(fileListJSON)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching PR files: %v\n", err)
		return false
	}

	// Step 2: Fetch the full diff
	diffInput := tool.GithubPRInput{
		Owner:    effectiveOwner,
		Repo:     effectiveRepo,
		Number:   *number,
		DiffOnly: true,
	}
	diffJSON, _ := json.Marshal(diffInput)
	diffResult, err := tool.ExecuteGithubPR(diffJSON)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching PR diff: %v\n", err)
		return false
	}

	// Step 3: Build the review prompt
	var review strings.Builder
	review.WriteString("## Automated Code Review\n\n")
	review.WriteString("### Changed Files\n\n")
	review.WriteString(fileListResult)
	review.WriteString("\n\n### Diff\n\n")
	review.WriteString(diffResult)
	review.WriteString("\n\n### Reviewer Instructions\n\n")
	review.WriteString(`Analyze the diff above and provide a thorough code review. For each file changed, evaluate:

1. **Bugs & Logic Errors** — Off-by-one errors, race conditions, nil pointer dereferences, incorrect error handling
2. **Security Issues** — SQL injection, command injection, hardcoded secrets, insecure crypto, path traversal, unsafe deserialization
3. **Go Best Practices** — Proper error wrapping, context propagation, goroutine lifecycle management, interface segregation, test coverage
4. **Performance Problems** — Unnecessary allocations, blocking calls in hot paths, missing caching, N+1 queries
5. **Style & Readability** — Naming conventions, unnecessary complexity, missing comments on exported symbols, consistency with project conventions

Format your response as:

## Summary
[Overall assessment: Look Good / Needs Work / Critical Issues]

## Issues Found

### {filename}:{line}
- **Severity**: {critical|major|minor|style}
- **Category**: {bug|security|performance|style}
- **Description**: [Clear description of the issue]
- **Suggestion**: [Specific fix recommendation]

## Strengths
[What the PR does well]

## Nitpicks
[Minor suggestions, not blocking]

Be concise and technical. Prefer action over explanation. Flag blocking issues with **CRITICAL** or **BLOCKING**.`)

	reviewResult := review.String()

	// Write to output file if specified, otherwise print to stdout
	if *output != "" {
		err := os.WriteFile(*output, []byte(reviewResult), 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error writing review to %s: %v\n", *output, err)
			return false
		}
		fmt.Fprintf(os.Stderr, "✅ Review written to %s (%d bytes)\n", *output, len(reviewResult))
	} else {
		fmt.Println(reviewResult)
	}

	return true
}
