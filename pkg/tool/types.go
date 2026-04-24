package tool

import (
	"encoding/json"

	deepseek "github.com/cohesion-org/deepseek-go"
	"github.com/invopop/jsonschema"
)

// Definitions is the list of all tool definitions.
var Definitions = []Definition{
	SmartReadFileDefinition,
	ReadFileDefinition,
	ListFilesDefinition,
	EditFileDefinition,
	SearchCodeDefinition,
	RunCommandDefinition,
	SearchKnowledgeDefinition,
	PineconeIngestDefinition,
	FrameworkStatusDefinition,
	CryptoTestDefinition,
	CryptoBuildDefinition,
	ListFrameworkComponentsDefinition,
	GitDiffDefinition,
	GithubPRDefinition,
	ProjectTreeDefinition,
	UTMValidateDefinition,
	ScaffoldModuleDefinition,
	FixCompileErrorsDefinition,
	BatchFixModulesDefinition,
}

// GenerateSchema generates a schema from a struct.
func GenerateSchema[T any]() *deepseek.FunctionParameters {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	var v T

	schema := reflector.Reflect(&v)

	properties := map[string]interface{}{}
	for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
		properties[pair.Key] = pair.Value
	}

	return &deepseek.FunctionParameters{
		Type:       "object",
		Properties: properties,
		Required:   schema.Required,
	}
}

// Definition is the definition for a tool.
type Definition struct {
	Name        string                       `json:"name,omitempty"`
	Description string                       `json:"description,omitempty"`
	InputSchema *deepseek.FunctionParameters `json:"input_schema,omitempty"`
	Function    func(input json.RawMessage) (string, error)
}
