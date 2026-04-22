package main

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
	"github.com/promacanthus/code-editing-agent/pkg/tool"
)

func main() {
	_ = godotenv.Load()

	providerFlag := flag.String("provider", envOrDefault("LLM_PROVIDER", ""), "Provider profile to use: deepseek or nvidia")
	workDir := flag.String("workdir", envOrDefault("LLM_WORKDIR", ""), "Path to the project directory the agent should operate on")
	baseURL := flag.String("base-url", envOrDefault("LLM_BASE_URL", ""), "Base URL for the chat-completions API")
	model := flag.String("model", envOrDefault("LLM_MODEL", ""), "Model name to use for chat completions")
	assistantName := flag.String("assistant-name", envOrDefault("LLM_ASSISTANT_NAME", ""), "Display name shown in the chat prompt")
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
	agent := agent.New(client, effectiveModel, effectiveAssistantName, getUserMessage, tool.Definitions)
	err = agent.Run(context.Background())
	if err != nil {
		fmt.Printf("Error: %s\n", err)
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
