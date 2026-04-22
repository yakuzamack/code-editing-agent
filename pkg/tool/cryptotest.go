package tool

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"
)

// CryptoTestDefinition is the definition for the crypto_test tool.
var CryptoTestDefinition = Definition{
	Name:        "crypto_test",
	Description: "Run tests for the crypto-framework. It specifically handles 'go test' configurations for this framework.",
	InputSchema: GenerateSchema[CryptoTestInput](),
	Function:    ExecuteCryptoTest,
}

// CryptoTestInput is the input for the crypto_test tool.
type CryptoTestInput struct {
	Package string `jsonschema:"description=The package to test (e.g., './pkg/implant/...'). Defaults to './...'"`
}

// ExecuteCryptoTest executes the crypto_test tool.
func ExecuteCryptoTest(input json.RawMessage) (string, error) {
	var args CryptoTestInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	pkg := args.Package
	if pkg == "" {
		pkg = "./..."
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "test", "-v", pkg)
	cmd.Dir = workingDir
	output, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return string(output) + "\n\nError: Test timed out after 60s", nil
	}

	if err != nil {
		return string(output) + "\n\nError: Tests failed: " + err.Error(), nil
	}

	return string(output), nil
}
