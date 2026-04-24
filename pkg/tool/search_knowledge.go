package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// SearchKnowledgeDefinition is the definition for the search_knowledge tool.
var SearchKnowledgeDefinition = Definition{
	Name:        "search_knowledge",
	Description: "Search the knowledge base (Pinecone) for relevant context, documentation, or code patterns. Use this when you need background information about the framework that is not available in the local directory.",
	InputSchema: GenerateSchema[SearchKnowledgeInput](),
	Function:    ExecuteSearchKnowledge,
}

// SearchKnowledgeInput is the input for the search_knowledge tool.
type SearchKnowledgeInput struct {
	Query string `json:"query" jsonschema:"description=The natural language search query to look up in the knowledge base."`
	// TopK is the number of results to return (default 5)
	TopK int `json:"top_k,omitempty" jsonschema:"description=Number of top results to return (default 5, max 20)."`
}

// pineconeQueryRequest is the Pinecone query API request body.
type pineconeQueryRequest struct {
	TopK          int                    `json:"topK"`
	Query         string                 `json:"query"`
	Filter        map[string]interface{} `json:"filter,omitempty"`
}

// pineconeQueryResponse is the Pinecone query API response body.
type pineconeQueryResponse struct {
	Matches []pineconeMatch `json:"matches"`
	Usage   map[string]int  `json:"usage,omitempty"`
}

type pineconeMatch struct {
	ID           string            `json:"id"`
	Score        float64           `json:"score"`
	Metadata     map[string]interface{} `json:"metadata"`
	SparseValues map[string]interface{} `json:"sparseValues,omitempty"`
}

// ExecuteSearchKnowledge executes the search_knowledge tool.
func ExecuteSearchKnowledge(input json.RawMessage) (string, error) {
	var args SearchKnowledgeInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	if args.Query == "" {
		return "Please provide a query to search the knowledge base.", nil
	}

	apiKey := os.Getenv("PINECONE_API_KEY")
	indexHost := os.Getenv("PINECONE_INDEX_HOST")

	if apiKey == "" || indexHost == "" {
		return "Pinecone search is not configured. Please set PINECONE_API_KEY and PINECONE_INDEX_HOST inside .env", nil
	}

	topK := args.TopK
	if topK <= 0 {
		topK = 5
	}
	if topK > 20 {
		topK = 20
	}

	// Build Pinecone query request
	reqBody := pineconeQueryRequest{
		Query: args.Query,
		TopK:  topK,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// POST to Pinecone's query endpoint
	queryURL := strings.TrimRight(indexHost, "/") + "/query"

	httpReq, err := http.NewRequest(http.MethodPost, queryURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Api-Key", apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Pinecone-API-Version", "2024-10")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to query Pinecone: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Pinecone API error (HTTP %d): %s", resp.StatusCode, string(respBody)), nil
	}

	var queryResp pineconeQueryResponse
	if err := json.Unmarshal(respBody, &queryResp); err != nil {
		// Return raw response if parsing fails
		return fmt.Sprintf("Pinecone response (unparseable): %s", string(respBody)), nil
	}

	if len(queryResp.Matches) == 0 {
		return fmt.Sprintf("No matching results found in the knowledge base for: %q", args.Query), nil
	}

	// Format results
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Knowledge Base Results for: %q\n\n", args.Query)
	fmt.Fprintf(&sb, "Found %d matches:\n\n", len(queryResp.Matches))

	for i, match := range queryResp.Matches {
		fmt.Fprintf(&sb, "### %d. %s (score: %.4f)\n", i+1, match.ID, match.Score)

		// Extract text content from metadata
		if text, ok := match.Metadata["text"].(string); ok && text != "" {
			// Truncate long text
			if len(text) > 2000 {
				text = text[:2000] + "\n...[truncated]..."
			}
			fmt.Fprintf(&sb, "%s\n\n", text)
		} else if source, ok := match.Metadata["source"].(string); ok {
			fmt.Fprintf(&sb, "Source: %s\n", source)
			if chunk, ok := match.Metadata["chunk_id"].(string); ok {
				fmt.Fprintf(&sb, "Chunk: %s\n", chunk)
			}
			fmt.Fprintf(&sb, "Score: %.4f\n\n", match.Score)
			// Print remaining metadata as key-value pairs
			for k, v := range match.Metadata {
				if k != "source" && k != "chunk_id" && k != "text" {
					fmt.Fprintf(&sb, "  %s: %v\n", k, v)
				}
			}
		} else {
			// Print all metadata as fallback
			for k, v := range match.Metadata {
				if k == "text" {
					textStr, _ := v.(string)
					if len(textStr) > 2000 {
						textStr = textStr[:2000] + "\n...[truncated]..."
					}
					fmt.Fprintf(&sb, "%s\n", textStr)
				} else {
					fmt.Fprintf(&sb, "  %s: %v\n", k, v)
				}
			}
			fmt.Fprintf(&sb, "\n")
		}
	}

	return sb.String(), nil
}
