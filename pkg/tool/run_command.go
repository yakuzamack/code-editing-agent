package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"time"
)

const maxCommandOutputLength = 12000

// RunCommandDefinition is the definition for the run_command tool.
var RunCommandDefinition = Definition{
	Name:        "run_command",
	Description: "Run a shell command inside the working directory. Use this for build, test, and framework commands.",
	InputSchema: RunCommandInputSchema,
	Function:    RunCommand,
}

// RunCommandInput is the input for the run_command tool.
type RunCommandInput struct {
	Command        string `json:"command" jsonschema_description:"Shell command to run inside the working directory."`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema_description:"Optional timeout in seconds. Defaults to 30 seconds and is capped at 600 seconds."`
}

// RunCommandResult is the output for the run_command tool.
type RunCommandResult struct {
	Command    string `json:"command"`
	WorkingDir string `json:"working_dir"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out"`
	Success    bool   `json:"success"`
}

// RunCommandInputSchema is the schema for the RunCommandInput struct.
var RunCommandInputSchema = GenerateSchema[RunCommandInput]()

// RunCommand runs a shell command in the configured working directory.
func RunCommand(input json.RawMessage) (string, error) {
	runCommandInput := RunCommandInput{}
	err := json.Unmarshal(input, &runCommandInput)
	if err != nil {
		return "", err
	}
	if runCommandInput.Command == "" {
		return "", errors.New("command is required")
	}

	timeoutSeconds := runCommandInput.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	if timeoutSeconds > 600 {
		timeoutSeconds = 600
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", runCommandInput.Command)
	cmd.Dir = WorkingDir()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	result := RunCommandResult{
		Command:    runCommandInput.Command,
		WorkingDir: WorkingDir(),
		ExitCode:   0,
		Success:    true,
	}

	err = cmd.Run()
	result.Stdout = truncateOutput(stdout.String(), maxCommandOutputLength)
	result.Stderr = truncateOutput(stderr.String(), maxCommandOutputLength)

	if err != nil {
		result.Success = false
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.TimedOut = true
			result.ExitCode = -1
		} else {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				result.ExitCode = exitErr.ExitCode()
			} else {
				return "", err
			}
		}
	}

	payload, err := json.Marshal(result)
	if err != nil {
		return "", err
	}

	return string(payload), nil
}

func truncateOutput(output string, limit int) string {
	if len(output) <= limit {
		return output
	}

	return output[:limit] + "\n... output truncated ..."
}
