package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ListFrameworkComponentsDefinition is the definition for the list_framework_components tool.
var ListFrameworkComponentsDefinition = Definition{
	Name:        "list_framework_components",
	Description: "List the main components of the crypto-framework (cmd, internal, pkg) to help understand the architecture.",
	InputSchema: GenerateSchema[ListFrameworkComponentsInput](),
	Function:    ExecuteListFrameworkComponents,
}

type ListFrameworkComponentsInput struct{}

func ExecuteListFrameworkComponents(input json.RawMessage) (string, error) {
	var result strings.Builder

	dirs := []string{"cmd", "internal", "pkg"}
	for _, d := range dirs {
		path := filepath.Join(workingDir, d)
		entries, err := os.ReadDir(path)
		if err != nil {
			result.WriteString(fmt.Sprintf("Error reading %s: %v\n", d, err))
			continue
		}

		result.WriteString(fmt.Sprintf("\n### %s/\n", d))
		for _, entry := range entries {
			if entry.IsDir() {
				result.WriteString(fmt.Sprintf("- %s/\n", entry.Name()))
			} else {
				result.WriteString(fmt.Sprintf("- %s\n", entry.Name()))
			}
		}
	}

	return result.String(), nil
}
