package tool

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// GithubListPRsDefinition is the definition for the github_list_prs tool.
var GithubListPRsDefinition = Definition{
	Name: "github_list_prs",
	Description: `List open GitHub Pull Requests for a repository. Use this BEFORE github_pr to discover which PRs need review.

Returns a numbered list of open PRs with:
  - PR number and title
  - Author and creation date
  - Labels and draft status
  - Changed files count (if brief=true, else omitted)

Once you have a PR number, use github_pr to fetch the full diff.
Requires GITHUB_TOKEN in .env with repo read scope.`,
	InputSchema: GenerateSchema[GithubListPRsInput](),
	Function:    ExecuteGithubListPRs,
}

// GithubListPRsInput is the input for the github_list_prs tool.
type GithubListPRsInput struct {
	// Owner is the GitHub repository owner (e.g. "promacanthus").
	Owner string `json:"owner" jsonschema:"description=GitHub repository owner (e.g. promacanthus)."`
	// Repo is the repository name (e.g. "crypto-framework").
	Repo string `json:"repo" jsonschema:"description=GitHub repository name (e.g. crypto-framework)."`
	// State filters by PR state: "open" (default), "closed", "all".
	State string `json:"state,omitempty" jsonschema:"description=Filter by state: 'open' (default), 'closed', 'all'."`
	// Labels filters by comma-separated label names (e.g., "bug,enhancement").
	Labels string `json:"labels,omitempty" jsonschema:"description=Comma-separated label names to filter (e.g., bug,enhancement)."`
	// Author filters by the GitHub login of the PR author.
	Author string `json:"author,omitempty" jsonschema:"description=Filter by PR author's GitHub username."`
	// MaxResults caps the number of PRs returned. Default 10, max 50.
	MaxResults int `json:"max_results,omitempty" jsonschema:"description=Maximum number of PRs to return. Default 10, max 50."`
	// Brief returns only PR number/title/author/date — no file counts or diffs.
	Brief bool `json:"brief,omitempty" jsonschema:"description=If true, return only PR number, title, author, and date (faster). Default false."`
}

// prSummary holds key info from the GitHub API.
type prSummary struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	Draft     bool      `json:"draft"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
		Color string `json:"color"`
	} `json:"labels"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

// ExecuteGithubListPRs lists open PRs for a repository.
func ExecuteGithubListPRs(input json.RawMessage) (string, error) {
	var args GithubListPRsInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	if args.Owner == "" || args.Repo == "" {
		return "Error: owner and repo are both required.", nil
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "Error: GITHUB_TOKEN is not set in your .env file. Add: GITHUB_TOKEN=ghp_...", nil
	}

	state := args.State
	if state == "" {
		state = "open"
	}

	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 50 {
		maxResults = 50
	}

	// Build query parameters
	var queryParts []string
	queryParts = append(queryParts, "state="+state)
	queryParts = append(queryParts, fmt.Sprintf("per_page=%d", maxResults))
	queryParts = append(queryParts, "sort=updated")
	queryParts = append(queryParts, "direction=desc")

	if args.Labels != "" {
		queryParts = append(queryParts, "labels="+args.Labels)
	}

	query := strings.Join(queryParts, "&")

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls?%s", args.Owner, args.Repo, query)

	body, err := githubAPIGet(url, token, "application/vnd.github+json")
	if err != nil {
		return "Error fetching PR list: " + err.Error(), nil
	}

	// Parse the response
	var prs []prSummary
	if err := json.Unmarshal([]byte(body), &prs); err != nil {
		return fmt.Sprintf("GitHub API response (unparseable):\n%s", body), nil
	}

	if len(prs) == 0 {
		return fmt.Sprintf("No %s pull requests found for %s/%s.", state, args.Owner, args.Repo), nil
	}

	// Optionally filter by author
	if args.Author != "" {
		var filtered []prSummary
		for _, pr := range prs {
			if strings.EqualFold(pr.User.Login, args.Author) {
				filtered = append(filtered, pr)
			}
		}
		prs = filtered
		if len(prs) == 0 {
			return fmt.Sprintf("No %s pull requests by %s for %s/%s.", state, args.Author, args.Owner, args.Repo), nil
		}
	}

	// Build the response
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Open Pull Requests — %s/%s\n\n", args.Owner, args.Repo)
	fmt.Fprintf(&sb, "Found **%d** %s PR(s):\n\n", len(prs), state)

	for i, pr := range prs {
		draftTag := ""
		if pr.Draft {
			draftTag = " 🏗️[DRAFT]"
		}

		// Label badges
		labelTags := ""
		if len(pr.Labels) > 0 {
			var labels []string
			for _, l := range pr.Labels {
				labels = append(labels, l.Name)
			}
			labelTags = " [`" + strings.Join(labels, "` `") + "`]"
		}

		fmt.Fprintf(&sb, "### %d. #%d — %s%s%s\n", i+1, pr.Number, pr.Title, draftTag, labelTags)
		fmt.Fprintf(&sb, "   **Author:** @%s  ", pr.User.Login)
		fmt.Fprintf(&sb, "**Branch:** `%s` → `%s`  ", pr.Head.Ref, pr.Base.Ref)
		fmt.Fprintf(&sb, "**Created:** %s  ", pr.CreatedAt.Format("2006-01-02"))
		fmt.Fprintf(&sb, "**Updated:** %s\n", pr.UpdatedAt.Format("2006-01-02"))

		if !args.Brief {
			// Fetch file count for this PR (lightweight HEAD request style)
			fileCount, err := getPRFileCount(pr.Number, args.Owner, args.Repo, token)
			if err == nil {
				fmt.Fprintf(&sb, "   **Files changed:** %d\n", fileCount)
			}
		}
		sb.WriteString("\n")
	}

	fmt.Fprintf(&sb, "---\n")
	fmt.Fprintf(&sb, "**To review a PR:** `github_pr --owner %s --repo %s --number <NUMBER>`\n", args.Owner, args.Repo)
	fmt.Fprintf(&sb, "**To list files only:** `github_pr --owner %s --repo %s --number <NUMBER> --files_only=true`\n", args.Owner, args.Repo)

	return sb.String(), nil
}

// getPRFileCount fetches just the file count from /pulls/{number}/files with a single page.
func getPRFileCount(prNumber int, owner, repo, token string) (int, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d/files?per_page=1", owner, repo, prNumber)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	// Parse Link header for pagination
	linkHeader := resp.Header.Get("Link")
	totalCount := parseLinkTotalCount(linkHeader)
	if totalCount > 0 {
		return totalCount, nil
	}

	// Fallback: count the response array length (won't be accurate beyond 1 page)
	var files []interface{}
	if err := json.Unmarshal(body, &files); err != nil {
		return 0, err
	}
	return len(files), nil
}

// parseLinkTotalCount extracts the last page number from a GitHub Link header.
// GitHub Link header format: <https://api.github.com/...?page=2>; rel="last"
func parseLinkTotalCount(linkHeader string) int {
	if linkHeader == "" {
		return 0
	}
	// Look for rel="last" and extract page=N
	parts := strings.Split(linkHeader, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.Contains(part, `rel="last"`) {
			// Extract the URL from <...>
			start := strings.Index(part, "<")
			end := strings.Index(part, ">")
			if start >= 0 && end > start {
				url := part[start+1 : end]
				// Extract page=N parameter
				for _, param := range strings.Split(url, "&") {
					if strings.HasPrefix(param, "page=") {
						var page int
						fmt.Sscanf(param, "page=%d", &page)
						// GitHub returns 1 page of up to 100 files
						// So total = last_page * 100 (per_page=100 by default, we use 1)
						// Actually we set per_page=1, so total = page * 1
						// But last page might be partial. For our heuristic:
						// page=last * per_page(100) since we use default per_page for the link header calc
						_ = page
						// Since we can't know exact count without fetching all pages,
						// return 0 to indicate "many files"
						return -1 // indicates "many"
					}
				}
			}
		}
	}
	return 0
}
