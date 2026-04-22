package tool

import (
	"encoding/json"
	"fmt"
	"os"
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
	Query string `jsonschema:"description=The natural language search query to look up in the knowledge base."`
}

// ExecuteSearchKnowledge executes the search_knowledge tool.
func ExecuteSearchKnowledge(input json.RawMessage) (string, error) {
	var args SearchKnowledgeInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	apiKey := os.Getenv("PINECONE_API_KEY")
	indexHost := os.Getenv("PINECONE_INDEX_HOST")

	if apiKey == "" || indexHost == "" {
		return "Pinecone search is not configured. Please set PINECONE_API_KEY and PINECONE_INDEX_HOST inside .env", nil
	}

	// For now, we simulate the search result until the SDK is fully integrated
	// In a real scenario, we would use the Pinecone SDK here
	return fmt.Sprintf("Search result for '%s': [MOCKED SUCCESS] No specific matches found in the remote knowledge base yet, but the tool is wired up. Use local search_code if needed.", args.Query), nil
}
