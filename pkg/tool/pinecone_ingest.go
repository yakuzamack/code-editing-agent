package tool

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PineconeIngestDefinition is the definition for the pinecone_ingest tool.
var PineconeIngestDefinition = Definition{
	Name:        "pinecone_ingest",
	Description: "Ingest (index/upsert) the crypto-framework documentation, source files, and scripts into the Pinecone knowledge base. Scans the framework directory for markdown, Go source, shell scripts, and READMEs, generates embeddings using NVIDIA's embedding API, and upserts them into Pinecone for semantic search.",
	InputSchema: GenerateSchema[PineconeIngestInput](),
	Function:    ExecutePineconeIngest,
}

// PineconeIngestInput is the input for the pinecone_ingest tool.
type PineconeIngestInput struct {
	// TargetDir is the path to the crypto-framework directory (defaults to LLM_WORKDIR env var)
	TargetDir string `json:"target_dir,omitempty" jsonschema:"description=Path to the crypto-framework directory to scan and index. Defaults to LLM_WORKDIR env var."`
	// FileTypes limits ingestion to specific file extensions (e.g., ".md,.go,.sh"). Default: .md,.go,.sh
	FileTypes string `json:"file_types,omitempty" jsonschema:"description=Comma-separated list of file extensions to include (e.g. '.md,.go,.sh'). Default: .md,.go,.sh,.yaml,.yml,.json,.toml"`
	// DryRun if true, only lists files that would be indexed without actually sending to Pinecone
	DryRun bool `json:"dry_run,omitempty" jsonschema:"description=If true, only list files that would be indexed without sending to Pinecone."`
	// ResetIndex if true, deletes all vectors in the Pinecone index before re-ingesting
	ResetIndex bool `json:"reset_index,omitempty" jsonschema:"description=If true, delete all vectors in the Pinecone index before re-ingesting."`
	// MaxChunkSize is the maximum size of each text chunk in characters (default: 2000)
	MaxChunkSize int `json:"max_chunk_size,omitempty" jsonschema:"description=Maximum chunk size in characters (default 2000)."`
	// ChunkOverlap is the overlap between consecutive chunks in characters (default: 200)
	ChunkOverlap int `json:"chunk_overlap,omitempty" jsonschema:"description=Overlap between chunks in characters (default 200)."`
	// BatchSize is the number of vectors to upsert in each batch (default: 50, max: 100)
	BatchSize int `json:"batch_size,omitempty" jsonschema:"description=Number of vectors to upsert per batch (default 50, max 100)."`
}

// embeddingRequest is the request body for NVIDIA embedding API.
type embeddingRequest struct {
	Input  string `json:"input"`
	Model  string `json:"model"`
	InputType string `json:"input_type,omitempty"`
}

// embeddingResponse is the response from NVIDIA embedding API.
type embeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// pineconeUpsertRequest is the Pinecone upsert API request body.
type pineconeUpsertRequest struct {
	Vectors []pineconeVector `json:"vectors"`
	Namespace string         `json:"namespace,omitempty"`
}

type pineconeVector struct {
	ID       string                 `json:"id"`
	Values   []float64              `json:"values"`
	Metadata map[string]interface{} `json:"metadata"`
}

// pineconeDeleteRequest is the Pinecone delete API request body.
type pineconeDeleteRequest struct {
	DeleteAll bool   `json:"deleteAll"`
	Namespace string `json:"namespace,omitempty"`
}

// chunkInfo holds a chunk of text with its source metadata.
type chunkInfo struct {
	ID       string
	Text     string
	FilePath string
	FileType string
	ChunkNum int
	TotalChunks int
}

// defaultExtensions lists the file extensions to index by default.
var defaultExtensions = map[string]bool{
	".md": true, ".go": true, ".sh": true,
	".yaml": true, ".yml": true, ".json": true, ".toml": true,
}

// ExecutePineconeIngest executes the pinecone_ingest tool.
func ExecutePineconeIngest(input json.RawMessage) (string, error) {
	var args PineconeIngestInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	// Resolve target directory
	targetDir := args.TargetDir
	if targetDir == "" {
		targetDir = os.Getenv("LLM_WORKDIR")
	}
	if targetDir == "" {
		return "No target directory specified. Set target_dir or LLM_WORKDIR env var.", nil
	}

	// Verify target exists
	info, err := os.Stat(targetDir)
	if err != nil {
		return "", fmt.Errorf("cannot access target directory %q: %w", targetDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", targetDir)
	}

	// Resolve file types filter
	extensions := defaultExtensions
	if args.FileTypes != "" {
		extensions = make(map[string]bool)
		for _, ext := range strings.Split(args.FileTypes, ",") {
			ext = strings.TrimSpace(ext)
			if ext != "" {
				if !strings.HasPrefix(ext, ".") {
					ext = "." + ext
				}
				extensions[ext] = true
			}
		}
	}

	// Chunk size defaults
	chunkSize := args.MaxChunkSize
	if chunkSize <= 0 {
		chunkSize = 2000
	}
	chunkOverlap := args.ChunkOverlap
	if chunkOverlap < 0 {
		chunkOverlap = 200
	}

	batchSize := args.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}
	if batchSize > 100 {
		batchSize = 100
	}

	// Check Pinecone credentials
	pineconeAPIKey := os.Getenv("PINECONE_API_KEY")
	pineconeIndexHost := os.Getenv("PINECONE_INDEX_HOST")
	if !args.DryRun && (pineconeAPIKey == "" || pineconeIndexHost == "") {
		return "Pinecone is not configured. Set PINECONE_API_KEY and PINECONE_INDEX_HOST in .env", nil
	}

	// Check embedding API credentials
	embedAPIKey := os.Getenv("NVIDIA_API_KEY")
	if embedAPIKey == "" {
		embedAPIKey = os.Getenv("OPENAI_API_KEY")
	}
	if !args.DryRun && embedAPIKey == "" {
		return "No API key found for embeddings. Set NVIDIA_API_KEY or OPENAI_API_KEY in .env", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## 📚 Pinecone Ingestion Report\n\n")
	fmt.Fprintf(&sb, "**Target directory:** `%s`\n", targetDir)
	fmt.Fprintf(&sb, "**File types:** %v\n", mapKeys(extensions))
	fmt.Fprintf(&sb, "**Chunk size:** %d chars (overlap: %d)\n", chunkSize, chunkOverlap)
	fmt.Fprintf(&sb, "**Batch size:** %d\n\n", batchSize)

	if args.DryRun {
		fmt.Fprintf(&sb, "> ⚠️ Dry-run mode — no data sent to Pinecone\n\n")
	}

	// Step 1: Collect all files
	fmt.Fprintf(&sb, "### Step 1: Scanning files\n\n")

	var files []string
	walkErr := filepath.Walk(targetDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible files
		}
		if fi.IsDir() {
			// Skip hidden directories and common exclusions
			base := fi.Name()
			if base == ".git" || base == ".vscode" || base == "node_modules" ||
				base == "__pycache__" || base == ".vitepress" || base == "public" ||
				base == "TODO" || base == "logs" || base == "data" || base == "bin" ||
				base == "build" || base == "artifacts" || base == ".cursor" ||
				base == ".diagnostic" || base == "testdata" || base == "stub" ||
				base == "github.com" {
				return filepath.SkipDir
			}
			// Skip dot-prefixed directories
			if strings.HasPrefix(base, ".") && base != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !extensions[ext] {
			return nil
		}
		// Skip large files (>1MB)
		if fi.Size() > 1*1024*1024 {
			fmt.Fprintf(&sb, "  ⏭️ Skipping large file: `%s` (%d MB)\n", relPath(targetDir, path), fi.Size()/1024/1024)
			return nil
		}
		files = append(files, path)
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("error scanning directory: %w", walkErr)
	}

	fmt.Fprintf(&sb, "Found **%d** files to index.\n\n", len(files))
	for _, f := range files {
		fmt.Fprintf(&sb, "- `%s`\n", relPath(targetDir, f))
	}

	if len(files) == 0 {
		fmt.Fprintf(&sb, "\nNo files found matching the specified file types.\n")
		return sb.String(), nil
	}

	// Step 2: Read and chunk files
	fmt.Fprintf(&sb, "\n### Step 2: Chunking content\n\n")

	var allChunks []chunkInfo
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(&sb, "  ⚠️ Error reading `%s`: %v\n", relPath(targetDir, file), err)
			continue
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}

		rel := relPath(targetDir, file)
		ext := strings.ToLower(filepath.Ext(file))
		chunks := chunkText(text, chunkSize, chunkOverlap)

		for i, chunk := range chunks {
			// Create a deterministic ID based on file path and chunk number
			id := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", rel, i))))
			allChunks = append(allChunks, chunkInfo{
				ID:           id[:32], // use first 32 hex chars
				Text:         chunk,
				FilePath:     rel,
				FileType:     ext,
				ChunkNum:     i + 1,
				TotalChunks:  len(chunks),
			})
		}
		fmt.Fprintf(&sb, "  ✅ `%s` → **%d** chunks\n", rel, len(chunks))
	}

	fmt.Fprintf(&sb, "\nTotal: **%d** chunks from **%d** files\n\n", len(allChunks), len(files))

	if args.DryRun {
		fmt.Fprintf(&sb, "> 🏁 Dry-run complete. No data was sent to Pinecone.\n")
		return sb.String(), nil
	}

	// Step 3 (optional): Reset the index
	if args.ResetIndex {
		fmt.Fprintf(&sb, "### Step 3: Resetting Pinecone index\n\n")
		err := pineconeDeleteAll(pineconeAPIKey, pineconeIndexHost)
		if err != nil {
			fmt.Fprintf(&sb, "  ❌ Failed to reset index: %v\n", err)
			return sb.String(), nil
		}
		fmt.Fprintf(&sb, "  ✅ Index cleared.\n\n")
	}

	// Step 4: Generate embeddings and upsert in batches
	fmt.Fprintf(&sb, "### Step 3: Generating embeddings and upserting to Pinecone\n\n")

	// Embedding endpoint
	embedURL := os.Getenv("NVIDIA_BASE_URL")
	if embedURL == "" {
		embedURL = "https://integrate.api.nvidia.com/v1/"
	}
	embedURL = strings.TrimRight(embedURL, "/") + "/embeddings"

	embedModel := "nvidia/nv-embed-qa-4" // NVIDIA's embedding model

	client := &http.Client{Timeout: 60 * time.Second}

	totalUpserted := 0
	for i := 0; i < len(allChunks); i += batchSize {
		end := i + batchSize
		if end > len(allChunks) {
			end = len(allChunks)
		}
		batch := allChunks[i:end]

		// Generate embeddings for each chunk in the batch
		var vectors []pineconeVector
		for _, chunk := range batch {
			embedding, err := generateEmbedding(client, embedURL, embedAPIKey, embedModel, chunk.Text)
			if err != nil {
				fmt.Fprintf(&sb, "  ⚠️ Embedding failed for `%s` chunk %d: %v\n", chunk.FilePath, chunk.ChunkNum, err)
				continue
			}

			metadata := map[string]interface{}{
				"text":         chunk.Text,
				"source":       chunk.FilePath,
				"file_type":    chunk.FileType,
				"chunk_id":     fmt.Sprintf("%d/%d", chunk.ChunkNum, chunk.TotalChunks),
				"chunk_num":    chunk.ChunkNum,
				"total_chunks": chunk.TotalChunks,
				"indexed_at":   time.Now().UTC().Format(time.RFC3339),
			}

			vectors = append(vectors, pineconeVector{
				ID:       chunk.ID,
				Values:   embedding,
				Metadata: metadata,
			})
		}

		if len(vectors) == 0 {
			continue
		}

		// Upsert to Pinecone
		err := pineconeUpsert(client, pineconeAPIKey, pineconeIndexHost, vectors)
		if err != nil {
			fmt.Fprintf(&sb, "  ❌ Upsert batch %d failed: %v\n", (i/batchSize)+1, err)
			continue
		}

		totalUpserted += len(vectors)
		fmt.Fprintf(&sb, "  ✅ Batch %d: upserted **%d** vectors\n", (i/batchSize)+1, len(vectors))
	}

	fmt.Fprintf(&sb, "\n### ✅ Summary\n\n")
	fmt.Fprintf(&sb, "- **Files scanned:** %d\n", len(files))
	fmt.Fprintf(&sb, "- **Total chunks:** %d\n", len(allChunks))
	fmt.Fprintf(&sb, "- **Vectors upserted:** %d\n", totalUpserted)
	if totalUpserted < len(allChunks) {
		fmt.Fprintf(&sb, "- **Failed/skipped:** %d\n", len(allChunks)-totalUpserted)
	}
	fmt.Fprintf(&sb, "- **Pinecone index:** `%s`\n", pineconeIndexHost)

	return sb.String(), nil
}

// generateEmbedding calls the NVIDIA embedding API to generate a vector for the given text.
func generateEmbedding(client *http.Client, url, apiKey, model, text string) ([]float64, error) {
	reqBody := embeddingRequest{
		Input:     text,
		Model:     model,
		InputType: "passage",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read embedding response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var embedResp embeddingResponse
	if err := json.Unmarshal(respBody, &embedResp); err != nil {
		return nil, fmt.Errorf("failed to parse embedding response: %w", err)
	}

	if len(embedResp.Data) == 0 {
		return nil, fmt.Errorf("embedding API returned no data")
	}

	return embedResp.Data[0].Embedding, nil
}

// pineconeUpsert upserts a batch of vectors to Pinecone.
func pineconeUpsert(client *http.Client, apiKey, indexHost string, vectors []pineconeVector) error {
	reqBody := pineconeUpsertRequest{
		Vectors: vectors,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal upsert request: %w", err)
	}

	url := strings.TrimRight(indexHost, "/") + "/vectors/upsert"

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create upsert request: %w", err)
	}
	httpReq.Header.Set("Api-Key", apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Pinecone-API-Version", "2024-10")

	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("upsert request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Pinecone upsert error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// pineconeDeleteAll deletes all vectors from the Pinecone index.
func pineconeDeleteAll(apiKey, indexHost string) error {
	client := &http.Client{Timeout: 30 * time.Second}

	reqBody := pineconeDeleteRequest{
		DeleteAll: true,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal delete request: %w", err)
	}

	url := strings.TrimRight(indexHost, "/") + "/vectors/delete"

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create delete request: %w", err)
	}
	httpReq.Header.Set("Api-Key", apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Pinecone-API-Version", "2024-10")

	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("delete request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Pinecone delete error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// chunkText splits text into overlapping chunks of approximately chunkSize characters.
func chunkText(text string, chunkSize, overlap int) []string {
	if len(text) <= chunkSize {
		return []string{text}
	}

	var chunks []string
	start := 0
	for start < len(text) {
		end := start + chunkSize
		if end > len(text) {
			end = len(text)
		}

		// Try to break at a newline boundary for cleaner chunks
		if end < len(text) {
			// Look backwards for a newline within the last 20% of the chunk
			lookBack := end
			searchStart := start + chunkSize*80/100
			if searchStart > start {
				lookBack = searchStart
			}
			if newlineAt := strings.LastIndex(text[lookBack:end], "\n"); newlineAt >= 0 {
				end = lookBack + newlineAt
			}
		}

		chunks = append(chunks, strings.TrimSpace(text[start:end]))

		// Move start forward, accounting for overlap
		start = end - overlap
		if start < 0 {
			start = 0
		}

		// Prevent infinite loop if chunk size is smaller than overlap
		if start >= end {
			break
		}
	}

	return chunks
}

// relPath returns a relative path from base to target.
func relPath(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}

// mapKeys returns the keys of a map as a slice.
func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
