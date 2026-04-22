# Code Editing Agent

[![Go Report Card](https://goreportcard.com/badge/github.com/promacanthus/code-editing-agent)](https://goreportcard.com/report/github.com/promacanthus/code-editing-agent)
[![Go Reference](https://pkg.go.dev/badge/github.com/promacanthus/code-editing-agent.svg)](https://pkg.go.dev/github.com/promacanthus/code-editing-agent)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/promacanthus/code-editing-agent)](https://github.com/promacanthus/code-editing-agent)
[![License](https://img.shields.io/github/license/promacanthus/code-editing-agent)](LICENSE)
[![GitHub last commit](https://img.shields.io/github/last-commit/promacanthus/code-editing-agent)](https://github.com/promacanthus/code-editing-agent/commits/main)
[![security: gosec](https://img.shields.io/badge/security-gosec-blue.svg)](https://github.com/securecodewarrior/gosec)
[![GitHub stars](https://img.shields.io/github/stars/promacanthus/code-editing-agent?style=social)](https://github.com/promacanthus/code-editing-agent)
[![GitHub forks](https://img.shields.io/github/forks/promacanthus/code-editing-agent?style=social)](https://github.com/promacanthus/code-editing-agent)

This project is a Go-based code editing agent that talks to OpenAI-compatible chat-completions APIs to interact with your codebase. It supports provider profiles so DeepSeek and NVIDIA can coexist in one local configuration.

## Architecture

![arch](arch.png)

## Features

* **Interactive Chat:** Chat with the agent to give it instructions.
* **File System Tools:** The agent can:
  * List files and directories.
  * Read the contents of files.
  * Edit files by replacing text.
    * Search code across the project.
    * Run shell commands inside the target project.
* **Provider Configurable:** The agent can use DeepSeek by default or any OpenAI-compatible endpoint you configure.

## How It Works

The agent works by sending your chat messages to a chat-completions API, along with a set of available tools. The configured model can then decide to use one of these tools to fulfill your request.

For example, if you ask the agent to "read the `main.go` file", it will call the `read_file` tool with the path `main.go`. The output of the tool will then be sent back to the DeepSeek model, which will use it to generate a response.

## Getting Started

### Prerequisites

* Go 1.24.5 or later
* An API key for your selected provider

### Installation

1. Clone the repository:

    ```bash
    git clone https://github.com/promacanthus/code-editing-agent.git
    ```

2. Navigate to the project directory:

    ```bash
    cd code-editing-agent
    ```

3. Install the dependencies:

    ```bash
    go mod tidy
    ```

### Configuration

Set your provider configuration in the environment before running the agent.

```bash
export DEEPSEEK_API_KEY=sk-your-api-key
```

The agent also auto-loads a local `.env` file from the project root, so you can store multiple providers and switch between them without retyping flags.

DeepSeek profile example:

```bash
export LLM_PROVIDER=deepseek
export DEEPSEEK_API_KEY=sk-your-api-key
export DEEPSEEK_MODEL=deepseek-chat
export DEEPSEEK_BASE_URL=https://api.deepseek.com/
export DEEPSEEK_ASSISTANT_NAME=DeepSeek
```

NVIDIA profile example:

```bash
export LLM_PROVIDER=nvidia
export NVIDIA_API_KEY=nvapi-your-api-key
export NVIDIA_MODEL=openai/gpt-oss-120b
export NVIDIA_BASE_URL=https://integrate.api.nvidia.com/v1/
export NVIDIA_ASSISTANT_NAME=NVIDIA
export LLM_WORKDIR=/Users/home/Projects/crypto-framework
```

Combined `.env` example:

```dotenv
LLM_PROVIDER=nvidia
LLM_WORKDIR=/Users/home/Projects/crypto-framework

DEEPSEEK_API_KEY=sk-your-deepseek-key
DEEPSEEK_BASE_URL=https://api.deepseek.com/
DEEPSEEK_MODEL=deepseek-chat
DEEPSEEK_ASSISTANT_NAME=DeepSeek

NVIDIA_API_KEY=nvapi-your-nvidia-key
NVIDIA_BASE_URL=https://integrate.api.nvidia.com/v1/
NVIDIA_MODEL=openai/gpt-oss-120b
NVIDIA_ASSISTANT_NAME=NVIDIA
```

### Usage

To run the agent, execute the following command:

```bash
go run main.go
```

You can then start chatting with the agent in your terminal.

To point the agent at a different project, pass `--workdir`:

```bash
go run main.go --workdir /Users/home/Projects/crypto-framework
```

If `.env` already contains `LLM_PROVIDER`, `LLM_WORKDIR`, and the selected provider's settings, a plain startup is enough:

```bash
go run main.go
```

You can switch providers at launch time too:

```bash
go run main.go \
    --provider nvidia \
    --workdir /Users/home/Projects/crypto-framework \
    --base-url https://integrate.api.nvidia.com/v1/ \
    --model openai/gpt-oss-120b \
    --assistant-name NVIDIA
```

Or switch back to DeepSeek without changing `.env`:

```bash
go run main.go --provider deepseek
```

## Tools

The following tools are available to the agent:

* `list_files`: List files and directories at a given path.
* `read_file`: Read the contents of a given file.
* `edit_file`: Make edits to a text file.
* `search_code`: Search for matching text across files in the working directory.
* `run_command`: Run a shell command in the working directory.

## Project Structure

```shell
.
├── go.mod
├── go.sum
├── main.go
└── pkg
    ├── agent
    │   └── agent.go
    └── tool
        ├── edit_file.go
        ├── list_files.go
        ├── read_file.go
        └── types.go
```

* `main.go`: The entry point of the application.
* `pkg/agent/agent.go`: Contains the core logic for the agent.
* `pkg/tool/`: Contains the definitions for the available tools.

## Acknowledgments

* This project is based on the tutorial [How to Build an Agent](https://ampcode.com/how-to-build-an-agent).
* The agent uses the [deepseek-go](https://github.com/cohesion-org/deepseek-go) library as a generic OpenAI-compatible chat client.
