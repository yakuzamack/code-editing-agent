package tool

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"
)

// CryptoBuildDefinition is the definition for the crypto_build tool.
var CryptoBuildDefinition = Definition{
	Name:        "crypto_build",
	Description: "Build a specific component of the crypto-framework (e.g., cmd/server, cmd/cli).",
	InputSchema: GenerateSchema[CryptoBuildInput](),
	Function:    ExecuteCryptoBuild,
}

type CryptoBuildInput struct {
	Component string `jsonschema:"description=The path to the component to build (e.g., './cmd/server')."`
	Target    string `jsonschema:"description=The output binary name (optional)."`
}

func ExecuteCryptoBuild(input json.RawMessage) (string, error) {
	var args CryptoBuildInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	if args.Component == "" {
		return "Error: Component path is required (e.g., './cmd/server')", nil
	}

	buildArgs := []string{"build"}
	if args.Target != "" {
		buildArgs = append(buildArgs, "-o", args.Target)
	}
	buildArgs = append(buildArgs, args.Component)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", buildArgs...)
	cmd.Dir = workingDir
	output, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return string(output) + "\n\nError: Build timed out after 120s", nil
	}

	if err != nil {
		return string(output) + "\n\nError: Build failed: " + err.Error(), nil
	}

	return "Build successful:\n" + string(output), nil
}
