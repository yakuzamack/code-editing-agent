package app

import (
	"bufio"
	"context"
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

// Configuration derived from flags and environment variables.
type Config struct {
	Provider      string
	WorkDir       string
	BaseURL       string
	Model         string
	AssistantName string
	MCPServers    string
	MCPTimeout    string
}

func loadConfig() Config {
	_ = godotenv.Load()
	providerFlag := flag.String("provider", envOrDefault("LLM_PROVIDER", ""), "Provider profile to use: deepseek or nvidia")
	workDir := flag.String("workdir", envOrDefault("LLM_WORKDIR", ""), "Path to the project directory the agent should operate on")
	baseURL := flag.String("base-url", envOrDefault("LLM_BASE_URL", ""), "Base URL for the chat-completions API")
	model := flag.String("model", envOrDefault("LLM_MODEL", ""), "Model name to use for chat completions")
	assistantName := flag.String("assistant-name", envOrDefault("LLM_ASSISTANT_NAME", ""), "Display name shown in the chat prompt")
	mcpServersFlag := flag.String("mcp-servers", envOrDefault("MCP_SERVERS", ""), "Comma-separated list of MCP servers to enable (e.g., go-lsp)")
	mcpTimeoutFlag := flag.String("mcp-timeout", envOrDefault("MCP_TIMEOUT", "30s"), "Timeout for MCP server operations (e.g., 30s, 2m)")
	flag.Parse()
	// Apply defaults where needed
	provider := normalizeProvider(*providerFlag)
	if provider == "" {
		provider = "deepseek"
	}
	cfg := Config{
		Provider:      provider,
		WorkDir:       *workDir,
		BaseURL:       *baseURL,
		Model:         *model,
		AssistantName: *assistantName,
		MCPServers:    *mcpServersFlag,
		MCPTimeout:    *mcpTimeoutFlag,
	}
	return cfg
}

func Run() error {
	cfg := loadConfig()
	// Resolve working directory
	if cfg.WorkDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("error getting cwd: %w", err)
		}
		cfg.WorkDir = cwd
	}
	if err := tool.SetWorkingDir(cfg.WorkDir); err != nil {
		return fmt.Errorf("error setting workdir: %w", err)
	}
	// Resolve API key
	apiKey := firstNonEmpty(os.Getenv("LLM_API_KEY"), providerAPIKey(cfg.Provider))
	if apiKey == "" {
		return fmt.Errorf("error: set a valid API key for provider %q", cfg.Provider)
	}
	// Resolve effective settings
	effectiveBaseURL := firstNonEmpty(cfg.BaseURL, providerEnv(cfg.Provider, "BASE_URL"), defaultBaseURL(cfg.Provider))
	effectiveModel := firstNonEmpty(cfg.Model, providerEnv(cfg.Provider, "MODEL"), defaultModel(cfg.Provider))
	effectiveAssistantName := firstNonEmpty(cfg.AssistantName, providerEnv(cfg.Provider, "ASSISTANT_NAME"), defaultAssistantName(cfg.Provider))

	fmt.Printf("Using provider: %s\n", cfg.Provider)
	fmt.Printf("Using working directory: %s\n", tool.WorkingDir())
	fmt.Printf("Using model: %s\n", effectiveModel)

	// Build HTTP client
	transport := http.DefaultTransport.(*http.Transport).Clone()
	httpClient := &http.Client{Transport: transport, Timeout: 2 * time.Minute}
	client, err := deepseek.NewClientWithOptions(apiKey, deepseek.WithBaseURL(normalizeBaseURL(effectiveBaseURL)), deepseek.WithHTTPClient(httpClient))
	if err != nil {
		return fmt.Errorf("error creating client: %w", err)
	}

	// Setup scanner for interactive input
	scanner := bufio.NewScanner(os.Stdin)
	getUserMessage := func() (string, bool) {
		if !scanner.Scan() {
			return "", false
		}
		return scanner.Text(), true
	}

	// Initialize MCP tools if requested
	allTools := tool.Definitions
	var mcpClient *mcp.Client
	if cfg.MCPServers != "" {
		timeoutDur, err := time.ParseDuration(cfg.MCPTimeout)
		if err != nil {
			fmt.Printf("Warning: invalid MCP_TIMEOUT %q, using default 30s\n", cfg.MCPTimeout)
			timeoutDur = 30 * time.Second
		}
		mcpClient = mcp.NewClient(timeoutDur)
		ctx := context.Background()
		serverNames := strings.Split(cfg.MCPServers, ",")
		var mcpTools []tool.Definition
		for _, name := range serverNames {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			switch name {
			case "go-lsp":
				if err := mcpClient.StartServer(ctx, "go-lsp", "gopls"); err != nil {
					fmt.Printf("Warning: failed to start gopls MCP server: %v\n", err)
					fmt.Println("  Install gopls with: go install golang.org/x/tools/gopls@latest")
					continue
				}
				// Register gopls typed tools
				goplsSpecific := mcp.GoLSPDefinitions(mcpClient)
				mcpTools = append(mcpTools, goplsSpecific...)
				// Discover additional tools via ListTools
				if goLspTools, err := mcpClient.ListTools(ctx, "go-lsp"); err != nil {
					fmt.Printf("Warning: failed to discover gopls tools: %v\n", err)
				} else {
					adapter := mcp.NewAdapterRegistry(mcpClient)
					for _, mt := range goLspTools {
						def := adapter.ConvertSingleTool(mt, "gopls")
						mcpTools = append(mcpTools, def)
					}
					fmt.Printf("Loaded %d tools from gopls\n", len(goLspTools))
				}
			default:
				fmt.Printf("Warning: unknown MCP server %q\n", name)
			}
		}
		if len(mcpTools) > 0 {
			allTools = mcp.MergeToolDefinitions(tool.Definitions, mcpTools)
			fmt.Printf("Total tools available: %d (built-in) + %d (MCP) = %d\n", len(tool.Definitions), len(mcpTools), len(allTools))
		}
	}

	ag := agent.New(client, effectiveModel, effectiveAssistantName, getUserMessage, allTools)
	if err := ag.Run(context.Background()); err != nil {
		return fmt.Errorf("agent run error: %w", err)
	}
	// Cleanup MCP client
	if mcpClient != nil {
		if err := mcpClient.Close(); err != nil {
			fmt.Printf("Warning: failed to close MCP client: %v\n", err)
		}
	}
	return nil
}

// Helper functions for configuration resolution.
func envOrDefault(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
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
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "", "deepseek":
		return p
	case "nvidia", "openai", "integrate":
		return "nvidia"
	default:
		return p
	}
}

func providerEnv(provider, suffix string) string {
	return os.Getenv(strings.ToUpper(provider) + "_" + suffix)
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
