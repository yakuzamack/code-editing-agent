package tool

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// GithubPRDefinition is the definition for the github_pr tool.
var GithubPRDefinition = Definition{
	Name:        "github_pr",
	Description: "Fetch details and the diff of a GitHub Pull Request. Use this to review a PR, understand what changed, and suggest or apply improvements. Requires GITHUB_TOKEN in .env.",
	InputSchema: GenerateSchema[GithubPRInput](),
	Function:    ExecuteGithubPR,
}

// GithubPRInput is the input for the github_pr tool.
type GithubPRInput struct {
	// Owner is the GitHub repository owner (e.g. "promacanthus").
	Owner string `json:"owner" jsonschema:"description=GitHub repository owner (e.g. promacanthus)."`
	// Repo is the repository name (e.g. "crypto-framework").
	Repo string `json:"repo" jsonschema:"description=GitHub repository name (e.g. crypto-framework)."`
	// Number is the PR number.
	Number int `json:"number" jsonschema:"description=The pull request number to review."`
	// DiffOnly fetches only the unified diff (no metadata) when true.
	DiffOnly bool `json:"diff_only,omitempty" jsonschema:"description=If true, return only the unified diff content without PR metadata."`
	// FilesOnly returns a compact list of changed files with stats (no diff content). Use this first on large PRs.
	FilesOnly bool `json:"files_only,omitempty" jsonschema:"description=If true, return only the list of changed files with add/delete counts. Much smaller than the full diff — use this first to identify which files to inspect."`
}

// ExecuteGithubPR fetches a PR's metadata and diff from the GitHub REST API.
func ExecuteGithubPR(input json.RawMessage) (string, error) {
	var args GithubPRInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	if args.Owner == "" || args.Repo == "" || args.Number == 0 {
		return "Error: owner, repo, and number are all required.", nil
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "Error: GITHUB_TOKEN is not set in your .env file. Add: GITHUB_TOKEN=ghp_...", nil
	}

	var result strings.Builder

	// FilesOnly: use /pulls/{number}/files endpoint for a compact file list.
	if args.FilesOnly {
		filesJSON, err := githubAPIGet(
			fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d/files?per_page=100", args.Owner, args.Repo, args.Number),
			token,
			"application/vnd.github+json",
		)
		if err != nil {
			return "Error fetching PR files: " + err.Error(), nil
		}
		var files []struct {
			Filename  string `json:"filename"`
			Status    string `json:"status"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
		}
		if err := json.Unmarshal([]byte(filesJSON), &files); err != nil {
			return filesJSON, nil // return raw if parse fails
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "## PR #%d Changed Files (%d files)\n\n", args.Number, len(files))
		for _, f := range files {
			fmt.Fprintf(&sb, "%-10s +%-4d -%-4d  %s\n", f.Status, f.Additions, f.Deletions, f.Filename)
		}
		return sb.String(), nil
	}

	if !args.DiffOnly {
		// Fetch PR metadata first
		meta, err := githubAPIGet(
			fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", args.Owner, args.Repo, args.Number),
			token,
			"application/vnd.github+json",
		)
		if err != nil {
			return "Error fetching PR metadata: " + err.Error(), nil
		}
		result.WriteString("## Pull Request Metadata\n")
		result.WriteString(meta)
		result.WriteString("\n\n")
	}

	// Fetch the unified diff
	diff, err := githubAPIGet(
		fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", args.Owner, args.Repo, args.Number),
		token,
		"application/vnd.github.diff",
	)
	if err != nil {
		return "Error fetching PR diff: " + err.Error(), nil
	}

	result.WriteString("## Diff\n```diff\n")
	if len(diff) > 20000 {
		result.WriteString(diff[:20000])
		result.WriteString("\n...[truncated, use DiffOnly=true and a smaller PR for full diff]\n")
	} else {
		result.WriteString(diff)
	}
	result.WriteString("\n```\n")

	return result.String(), nil
}

// githubAPIGet performs a GET request against the GitHub API with the given Accept header.
func githubAPIGet(url, token, accept string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// Log error but don't fail the operation
			fmt.Printf("Warning: failed to close response body: %v\n", err)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("GitHub API error %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}
