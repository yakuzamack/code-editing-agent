package tool

import (
	"encoding/json"
	"fmt"
	"os"
)

// ReadFileDefinition is the definition for the read_file tool.
var ReadFileDefinition = Definition{
	Name:        "read_file",
	Description: "Read the contents of a given relative file path. Use this when you want to see what's inside a file. Do not use this with directory names.",
	InputSchema: ReadFileInputSchema,
	Function:    ReadFile,
}

// ReadFileInput is the input for the read_file tool.
type ReadFileInput struct {
	Path string `json:"path" jsonschema_description:"The relative path of a file in the working directory."`
}

// ReadFileInputSchema is the schema for the ReadFileInput struct.
var ReadFileInputSchema = GenerateSchema[ReadFileInput]()

const readFileMaxBytes = 50_000

// ReadFile reads the contents of a file.
func ReadFile(input json.RawMessage) (string, error) {
	readFileInput := ReadFileInput{}
	err := json.Unmarshal(input, &readFileInput)
	if err != nil {
		return "", err
	}

	resolvedPath, err := resolvePath(readFileInput.Path)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return "", err
	}
	if len(content) > readFileMaxBytes {
		return fmt.Sprintf("%s\n[...file truncated at %d bytes — use smart_read_file with line_start/line_end or symbol to read specific sections...]",
			content[:readFileMaxBytes], readFileMaxBytes), nil
	}
	return string(content), nil
}
