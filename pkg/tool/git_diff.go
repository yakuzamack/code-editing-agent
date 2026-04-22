package tool

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"
)

// GitDiffDefinition is the definition for the git_diff tool.
var GitDiffDefinition = Definition{
	Name:        "git_diff",
	Description: "Show git diffs for the working directory. Use this to inspect uncommitted changes, compare branches, or review a specific file's diff before or after an edit. Equivalent to running 'git diff' or 'git diff HEAD~1'.",
	InputSchema: GenerateSchema[GitDiffInput](),
	Function:    ExecuteGitDiff,
}

// GitDiffInput is the input for the git_diff tool.
type GitDiffInput struct {
	// Ref is an optional git ref like "HEAD~1", "main", or a commit SHA to diff against.
	Ref string `jsonschema:"description=Optional git ref to diff against (e.g. HEAD~1 or main). Leave empty for unstaged changes."`
	// Path is an optional file path to limit the diff to.
	Path string `jsonschema:"description=Optional file path to limit the diff to (e.g. internal/server/api.go)."`
	// Staged shows only staged (index) changes when true.
	Staged bool `jsonschema:"description=If true, show only staged changes (git diff --cached)."`
	// NameOnly returns only the list of changed file names without diff content. Use this first on large PRs to discover which files changed.
	NameOnly bool `jsonschema:"description=If true, return only the list of changed file names (git diff --name-only). Useful to survey large PRs before reading specific file diffs."`
}

// ExecuteGitDiff runs a git diff in the working directory and returns the output.
func ExecuteGitDiff(input json.RawMessage) (string, error) {
	var args GitDiffInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	gitArgs := []string{"diff"}
	if args.NameOnly {
		gitArgs = append(gitArgs, "--name-only")
	} else {
		gitArgs = append(gitArgs, "--stat", "-p")
	}

	if args.Staged {
		gitArgs = append(gitArgs, "--cached")
	}

	if args.Ref != "" {
		gitArgs = append(gitArgs, args.Ref)
	}

	if args.Path != "" {
		resolved, err := resolvePath(args.Path)
		if err != nil {
			return "", err
		}
		gitArgs = append(gitArgs, "--", resolved)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	cmd.Dir = workingDir
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		return "Error running git diff: " + err.Error(), nil
	}
	if len(output) == 0 {
		return "No changes detected.", nil
	}
	result := string(output)
	// Truncate to avoid overflowing context
	if len(result) > 16000 {
		result = result[:16000] + "\n\n[...truncated, diff too large. Use path parameter to narrow scope.]"
	}
	return result, nil
}
