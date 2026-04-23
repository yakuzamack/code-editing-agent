package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// UTMValidateDefinition is the definition for the utm_validate tool.
var UTMValidateDefinition = Definition{
	Name: "utm_validate",
	Description: `Orchestrate UTM Windows VM end-to-end validation for the crypto-framework.

Phases:
  preflight      — check utmctl, list VMs, run init-utm-host.sh (validates manifest + dirs).
  start_vm       — start the UTM VM if it's currently stopped.
  build_beacon   — rsync repo to UTM share and build beacon-utm.exe inside the Windows guest.
  deploy_beacon  — start the beacon executable inside the Windows VM.
  wait_sessions  — wait for beacon to establish connection and active sessions.
  run_audit      — run the monitored module audit (run-utm-audit-monitored.sh) and return the summary.
  check_sessions — query the Admin API for active sessions and task results.
  full           — preflight → start_vm → build_beacon → deploy_beacon → wait_sessions → run_audit → check_sessions in sequence.

Environment variables loaded automatically from the framework .env if present.
Required for build_beacon / full: vm_name and share_dir will default to sensible values if not provided.
Required for check_sessions / run_audit: api_secret must be supplied or present in .env as CRYPTOFRAMEWORK_API_SECRET / VIRGA_AUTH_SECRET.`,
	InputSchema: UTMValidateInputSchema,
	Function:    UTMValidate,
}

// UTMValidateInput is the input for the utm_validate tool.
type UTMValidateInput struct {
	// Phase selects which step(s) to run.
	Phase string `json:"phase" jsonschema_description:"One of: preflight, build_beacon, run_audit, check_sessions, full."`

	// VMName is the UTM VM name or UUID (used in CRYPTOFRAMEWORK_UTM_VM). Defaults to 'Windows 2'.
	VMName string `json:"vm_name,omitempty" jsonschema_description:"UTM VM name or UUID. Defaults to 'Windows 2'."`

	// ShareDir is the macOS path of the UTM Share Directory (CRYPTOFRAMEWORK_UTM_SHARE).
	// Required for build_beacon. Ignored for preflight/check_sessions. Defaults to ~/UTM-crypto-audit.
	ShareDir string `json:"share_dir,omitempty" jsonschema_description:"macOS path of UTM Share Directory (CRYPTOFRAMEWORK_UTM_SHARE). Defaults to ~/UTM-crypto-audit."`

	// ApiSecret overrides CRYPTOFRAMEWORK_API_SECRET for Admin API calls.
	ApiSecret string `json:"api_secret,omitempty" jsonschema_description:"Admin API secret (CRYPTOFRAMEWORK_API_SECRET). Falls back to value in framework .env."`

	// AdminURL overrides CRYPTOFRAMEWORK_ADMIN_URL. Defaults to http://127.0.0.1:8443.
	AdminURL string `json:"admin_url,omitempty" jsonschema_description:"Admin API base URL. Defaults to http://127.0.0.1:8443."`

	// SkipPreview skips the slow UTM/AppleScript guest process preview in run_audit.
	SkipPreview bool `json:"skip_preview,omitempty" jsonschema_description:"Set true to skip the utmctl guest preview step (faster)."`

	// TimeoutSeconds caps the whole operation. Defaults to 300 (5 min), max 1200.
	TimeoutSeconds int `json:"timeout_seconds,omitempty" jsonschema_description:"Total timeout in seconds. Defaults to 300, max 1200."`
}

// UTMValidateInputSchema is the schema for the UTMValidateInput struct.
var UTMValidateInputSchema = GenerateSchema[UTMValidateInput]()

const maxUTMOutputLength = 16000

// UTMValidate orchestrates UTM Windows VM E2E validation.
func UTMValidate(input json.RawMessage) (string, error) {
	var in UTMValidateInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if in.Phase == "" {
		return "", fmt.Errorf("phase is required: preflight | build_beacon | run_audit | check_sessions | full")
	}

	timeoutSec := in.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 600 // Increased default timeout for VM operations
	}
	if timeoutSec > 1800 {
		timeoutSec = 1800 // Increased max timeout for full end-to-end testing
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	// Use crypto-framework directory from LLM_WORKDIR, not the current working directory
	fw := os.Getenv("LLM_WORKDIR")
	if fw == "" {
		// Try to read from .env file in current directory
		if envData, err := os.ReadFile(".env"); err == nil {
			for _, line := range strings.Split(string(envData), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "LLM_WORKDIR=") {
					fw = strings.Trim(strings.TrimPrefix(line, "LLM_WORKDIR="), `"'`)
					break
				}
			}
		}
	}
	if fw == "" {
		fw = WorkingDir() // fallback to current working directory
	}
	env := utmBuildEnv(fw, &in)

	var sb strings.Builder

	switch in.Phase {
	case "preflight":
		runPhase(ctx, fw, env, &sb, "preflight", func() error {
			return utmPreflight(ctx, fw, env, &sb)
		})
	case "start_vm":
		runPhase(ctx, fw, env, &sb, "start_vm", func() error {
			return utmStartVM(ctx, fw, env, &sb, &in)
		})
	case "build_beacon":
		runPhase(ctx, fw, env, &sb, "build_beacon", func() error {
			return utmBuildBeacon(ctx, fw, env, &sb, &in)
		})
	case "deploy_beacon":
		runPhase(ctx, fw, env, &sb, "deploy_beacon", func() error {
			return utmDeployBeacon(ctx, fw, env, &sb, &in)
		})
	case "wait_sessions":
		runPhase(ctx, fw, env, &sb, "wait_sessions", func() error {
			return utmWaitSessions(ctx, fw, env, &sb, &in)
		})
	case "run_audit":
		runPhase(ctx, fw, env, &sb, "run_audit", func() error {
			return utmRunAudit(ctx, fw, env, &sb, &in)
		})
	case "check_sessions":
		runPhase(ctx, fw, env, &sb, "check_sessions", func() error {
			return utmCheckSessions(ctx, fw, env, &sb)
		})
	case "full":
		phases := []struct {
			name string
			fn   func() error
		}{
			{"preflight", func() error { return utmPreflight(ctx, fw, env, &sb) }},
			{"start_vm", func() error { return utmStartVM(ctx, fw, env, &sb, &in) }},
			{"build_beacon", func() error { return utmBuildBeacon(ctx, fw, env, &sb, &in) }},
			{"deploy_beacon", func() error { return utmDeployBeacon(ctx, fw, env, &sb, &in) }},
			{"wait_sessions", func() error { return utmWaitSessions(ctx, fw, env, &sb, &in) }},
			{"run_audit", func() error { return utmRunAudit(ctx, fw, env, &sb, &in) }},
			{"check_sessions", func() error { return utmCheckSessions(ctx, fw, env, &sb) }},
		}
		for _, p := range phases {
			if ctx.Err() != nil {
				fmt.Fprintf(&sb, "\n[ABORTED] Context expired before phase: %s\n", p.name)
				break
			}
			runPhase(ctx, fw, env, &sb, p.name, p.fn)
		}
	default:
		return "", fmt.Errorf("unknown phase %q: choose preflight | start_vm | build_beacon | deploy_beacon | wait_sessions | run_audit | check_sessions | full", in.Phase)
	}

	return truncateOutput(sb.String(), maxUTMOutputLength), nil
}

// runPhase writes a banner, calls fn, and appends any error.
func runPhase(_ context.Context, _ string, _ []string, sb *strings.Builder, name string, fn func() error) {
	fmt.Fprintf(sb, "\n=== Phase: %s ===\n", name)
	if err := fn(); err != nil {
		fmt.Fprintf(sb, "[ERROR] %s\n", err)
	}
}

// utmBuildEnv merges os environment with UTM-specific overrides derived from input.
func utmBuildEnv(fw string, in *UTMValidateInput) []string {
	// Start from a clean copy of current process env.
	base := os.Environ()

	extras := map[string]string{}

	// Load framework .env if present so VIRGA_AUTH_SECRET etc. are available.
	envFile := filepath.Join(fw, ".env")
	if data, err := os.ReadFile(envFile); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if k, v, ok := strings.Cut(line, "="); ok {
				extras[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
	}

	if in.VMName != "" {
		extras["CRYPTOFRAMEWORK_UTM_VM"] = in.VMName
	} else if extras["CRYPTOFRAMEWORK_UTM_VM"] == "" {
		extras["CRYPTOFRAMEWORK_UTM_VM"] = "Windows 2"
	}

	if in.ShareDir != "" {
		expanded := expandPath(in.ShareDir)
		extras["CRYPTOFRAMEWORK_UTM_SHARE"] = expanded
	} else if extras["CRYPTOFRAMEWORK_UTM_SHARE"] == "" {
		// Set sensible default
		home, _ := os.UserHomeDir()
		extras["CRYPTOFRAMEWORK_UTM_SHARE"] = filepath.Join(home, "UTM-crypto-audit")
	}

	if in.ApiSecret != "" {
		extras["CRYPTOFRAMEWORK_API_SECRET"] = in.ApiSecret
	}
	// Fall back: derive CRYPTOFRAMEWORK_API_SECRET from VIRGA_AUTH_SECRET when only the latter is set.
	if extras["CRYPTOFRAMEWORK_API_SECRET"] == "" && extras["VIRGA_AUTH_SECRET"] != "" {
		extras["CRYPTOFRAMEWORK_API_SECRET"] = extras["VIRGA_AUTH_SECRET"]
	}

	if in.AdminURL != "" {
		extras["CRYPTOFRAMEWORK_ADMIN_URL"] = in.AdminURL
	}

	if in.SkipPreview {
		extras["SKIP_UTM_PREVIEW"] = "1"
	}

	// Build final env slice: override existing keys, append new ones.
	overridden := map[string]bool{}
	result := make([]string, 0, len(base)+len(extras))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if v, ok := extras[k]; ok {
			result = append(result, k+"="+v)
			overridden[k] = true
		} else {
			result = append(result, kv)
		}
	}
	for k, v := range extras {
		if !overridden[k] {
			result = append(result, k+"="+v)
		}
	}
	return result
}

// utmPreflight checks utmctl availability, lists VMs, and runs init-utm-host.sh.
func utmPreflight(ctx context.Context, fw string, env []string, sb *strings.Builder) error {
	// Check utmctl
	out, err := runScript(ctx, fw, env, "utmctl list")
	sb.WriteString("--- utmctl list ---\n")
	sb.WriteString(out)
	if err != nil {
		fmt.Fprintf(sb, "(utmctl error: %v — install via: sudo ln -sf /Applications/UTM.app/Contents/MacOS/utmctl /usr/local/bin/utmctl)\n", err)
	}

	// Run init-utm-host.sh
	initScript := filepath.Join(fw, "scripts/dev/utm-audit/init-utm-host.sh")
	if _, statErr := os.Stat(initScript); statErr == nil {
		sb.WriteString("\n--- init-utm-host.sh ---\n")
		out2, err2 := runScript(ctx, fw, env, "bash "+initScript)
		sb.WriteString(out2)
		if err2 != nil {
			return fmt.Errorf("init-utm-host.sh: %w", err2)
		}
	} else {
		sb.WriteString("(init-utm-host.sh not found — skipped)\n")
	}
	return nil
}

// utmBuildBeacon rsyncs the repo to the UTM share and builds beacon-utm.exe in the Windows guest.
func utmBuildBeacon(ctx context.Context, fw string, env []string, sb *strings.Builder, in *UTMValidateInput) error {
	script := filepath.Join(fw, "scripts/dev/utm-audit/host-build-beacon-utm-share.sh")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("host-build-beacon-utm-share.sh not found at %s", script)
	}

	share := utmEnvVal(env, "CRYPTOFRAMEWORK_UTM_SHARE")
	if share == "" {
		return fmt.Errorf("share_dir is required for build_beacon (set share_dir parameter or CRYPTOFRAMEWORK_UTM_SHARE env)")
	}
	vm := utmEnvVal(env, "CRYPTOFRAMEWORK_UTM_VM")
	fmt.Fprintf(sb, "VM: %s  Share: %s\n", vm, share)

	out, err := runScript(ctx, fw, env, "bash "+script)
	sb.WriteString(out)
	return err
}

// utmRunAudit runs the monitored module audit script and appends the summary.
func utmRunAudit(ctx context.Context, fw string, env []string, sb *strings.Builder, in *UTMValidateInput) error {
	script := filepath.Join(fw, "scripts/dev/utm-audit/run-utm-audit-monitored.sh")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("run-utm-audit-monitored.sh not found at %s", script)
	}

	apiSecret := utmEnvVal(env, "CRYPTOFRAMEWORK_API_SECRET")
	if apiSecret == "" {
		return fmt.Errorf("api_secret is required for run_audit (set api_secret parameter or CRYPTOFRAMEWORK_API_SECRET / VIRGA_AUTH_SECRET in .env)")
	}

	out, err := runScript(ctx, fw, env, "bash "+script)
	sb.WriteString(out)
	return err
}

// utmCheckSessions queries the Admin API for active sessions.
// Auth flow: POST /api/auth/login with VIRGA_AUTH_SECRET → JWT → Bearer token for /api/sessions.
func utmCheckSessions(ctx context.Context, fw string, env []string, sb *strings.Builder) error {
	// Prefer explicit override, then fall back to VIRGA_AUTH_SECRET (what the server reads).
	apiSecret := utmEnvVal(env, "CRYPTOFRAMEWORK_API_SECRET")
	if apiSecret == "" {
		apiSecret = utmEnvVal(env, "VIRGA_AUTH_SECRET")
	}
	adminURL := utmEnvVal(env, "CRYPTOFRAMEWORK_ADMIN_URL")
	if adminURL == "" {
		adminURL = "http://127.0.0.1:8443"
	}

	if apiSecret == "" {
		return fmt.Errorf("api_secret / VIRGA_AUTH_SECRET is required for check_sessions")
	}

	// Step 1: login to get JWT token.
	loginCmd := fmt.Sprintf(
		`curl -sf -X POST -H "Content-Type: application/json" -d '{"secret":"%s"}' %s/api/login`,
		apiSecret, adminURL,
	)
	loginOut, err := runScript(ctx, fw, env, loginCmd)
	sb.WriteString("--- login ---\n")
	sb.WriteString(loginOut + "\n")
	if err != nil {
		return fmt.Errorf("login failed (is the server running at %s?): %w", adminURL, err)
	}

	// Extract token from {"token":"..."} response.
	var loginResp struct {
		Token string `json:"token"`
	}
	if jsonErr := json.Unmarshal([]byte(loginOut), &loginResp); jsonErr != nil || loginResp.Token == "" {
		return fmt.Errorf("could not parse login token from response: %s", loginOut)
	}

	// Step 2: fetch sessions with Bearer token.
	sessionsCmd := fmt.Sprintf(
		`curl -sf -H "Authorization: Bearer %s" %s/api/sessions`,
		loginResp.Token, adminURL,
	)
	out, err := runScript(ctx, fw, env, sessionsCmd)
	sb.WriteString("\n--- sessions ---\n")
	sb.WriteString(out)
	if err != nil {
		return fmt.Errorf("sessions query failed: %w", err)
	}

	// Also try the CLI if available.
	cli := filepath.Join(fw, "bin/crypto-framework-cli")
	if _, statErr := os.Stat(cli); statErr == nil {
		sb.WriteString("\n--- cli sessions list ---\n")
		out2, _ := runScript(ctx, fw, env,
			fmt.Sprintf(`%s -e "sessions list"`, cli),
		)
		sb.WriteString(out2)
	}

	return nil
}

// utmStartVM starts the UTM VM if it's currently stopped.
func utmStartVM(ctx context.Context, fw string, env []string, sb *strings.Builder, in *UTMValidateInput) error {
	vm := utmEnvVal(env, "CRYPTOFRAMEWORK_UTM_VM")
	if vm == "" {
		return fmt.Errorf("vm_name is required for start_vm (set vm_name parameter or CRYPTOFRAMEWORK_UTM_VM env)")
	}

	// Check current VM status
	fmt.Fprintf(sb, "Checking VM status: %s\n", vm)
	out, err := runScript(ctx, fw, env, "utmctl list")
	sb.WriteString("--- utmctl list ---\n")
	sb.WriteString(out)
	if err != nil {
		return fmt.Errorf("utmctl list failed: %w", err)
	}

	// Parse status - look for VM in output
	if !strings.Contains(out, vm) {
		return fmt.Errorf("VM %q not found in utmctl list output", vm)
	}

	// Check if already running
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, vm) && strings.Contains(line, "started") {
			fmt.Fprintf(sb, "VM %s is already running - skipping start\n", vm)
			return nil
		}
	}

	// Start the VM
	fmt.Fprintf(sb, "Starting VM: %s\n", vm)
	startOut, err := runScript(ctx, fw, env, fmt.Sprintf("utmctl start %q", vm))
	sb.WriteString("--- utmctl start ---\n")
	sb.WriteString(startOut)
	if err != nil {
		return fmt.Errorf("failed to start VM %q: %w", vm, err)
	}

	// Wait for VM to be fully started (give it some time to boot)
	sb.WriteString("Waiting for VM to start up (30 seconds)...\n")
	select {
	case <-ctx.Done():
		return fmt.Errorf("context canceled while waiting for VM startup")
	case <-time.After(30 * time.Second):
		// Continue
	}

	// Verify VM is now running
	verifyOut, err := runScript(ctx, fw, env, "utmctl list")
	sb.WriteString("--- verify running ---\n")
	sb.WriteString(verifyOut)
	if err == nil {
		for _, line := range strings.Split(verifyOut, "\n") {
			if strings.Contains(line, vm) && strings.Contains(line, "started") {
				fmt.Fprintf(sb, "✅ VM %s successfully started\n", vm)
				return nil
			}
		}
	}

	sb.WriteString("⚠️  VM start command completed but status unclear - continuing anyway\n")
	return nil
}

// utmDeployBeacon starts the beacon executable inside the Windows VM.
func utmDeployBeacon(ctx context.Context, fw string, env []string, sb *strings.Builder, in *UTMValidateInput) error {
	vm := utmEnvVal(env, "CRYPTOFRAMEWORK_UTM_VM")
	if vm == "" {
		return fmt.Errorf("vm_name is required for deploy_beacon")
	}

	// Default beacon path in the VM - assumes beacon was built by build_beacon phase
	beaconPath := "Z:\\crypto-audit\\beacon-utm.exe"

	script := filepath.Join(fw, "scripts/dev/utm-audit/run-beacon-utm-guest.sh")
	if _, err := os.Stat(script); err != nil {
		// If the script doesn't exist, use a simpler approach via utmctl
		sb.WriteString("Using direct utmctl approach for beacon deployment\n")
		return utmDeployBeaconDirect(ctx, fw, env, sb, vm, beaconPath)
	}

	fmt.Fprintf(sb, "Deploying beacon in VM %s using script: %s\n", vm, script)

	// Set environment for the beacon deployment script
	beaconEnv := append(env,
		"UTM_WINDOWS_BEACON_PATH="+beaconPath,
	)

	out, err := runScript(ctx, fw, beaconEnv, "bash "+script)
	sb.WriteString("--- beacon deployment ---\n")
	sb.WriteString(out)

	if err != nil {
		fmt.Fprintf(sb, "⚠️  Beacon deployment script returned error: %v\n", err)
		// Try direct approach as fallback
		return utmDeployBeaconDirect(ctx, fw, env, sb, vm, beaconPath)
	}

	sb.WriteString("✅ Beacon deployment script completed\n")
	return nil
}

// utmDeployBeaconDirect uses utmctl to run the beacon directly via applescript.
func utmDeployBeaconDirect(ctx context.Context, fw string, env []string, sb *strings.Builder, vm, beaconPath string) error {
	// Use AppleScript via utmctl to execute the beacon
	// This matches the pattern used in the existing scripts
	osascript := fmt.Sprintf(`
		tell application "UTM"
			set vmByName to first virtual machine whose name is "%s"
			set vmDocument to document of vmByName
			tell vmDocument
				-- Check if VM is running
				if not (get started) then
					error "VM is not running"
				end if

				-- Send keystrokes to run the beacon
				type text "cd Z:\\crypto-audit && beacon-utm.exe"
				type key "return"
			end tell
		end tell
	`, vm)

	cmd := fmt.Sprintf(`osascript -e '%s'`, strings.ReplaceAll(osascript, "'", "\\'"))

	sb.WriteString("Executing beacon via AppleScript...\n")
	out, err := runScript(ctx, fw, env, cmd)
	sb.WriteString("--- applescript output ---\n")
	sb.WriteString(out)

	if err != nil {
		fmt.Fprintf(sb, "⚠️  AppleScript execution failed: %v\n", err)
		sb.WriteString("Note: You may need to grant VS Code Automation permissions in System Preferences\n")
		sb.WriteString("Or manually run the beacon in the Windows VM\n")
		return err
	}

	sb.WriteString("✅ Beacon started via AppleScript\n")
	return nil
}

// utmWaitSessions waits for beacon to establish connection and create active sessions.
func utmWaitSessions(ctx context.Context, fw string, env []string, sb *strings.Builder, in *UTMValidateInput) error {
	apiSecret := utmEnvVal(env, "CRYPTOFRAMEWORK_API_SECRET")
	if apiSecret == "" {
		apiSecret = utmEnvVal(env, "VIRGA_AUTH_SECRET")
	}
	adminURL := utmEnvVal(env, "CRYPTOFRAMEWORK_ADMIN_URL")
	if adminURL == "" {
		adminURL = "http://127.0.0.1:8443"
	}

	if apiSecret == "" {
		sb.WriteString("❌ Missing API secret configuration\n")
		sb.WriteString("Please set VIRGA_AUTH_SECRET in your crypto-framework .env file:\n")
		sb.WriteString("  cd /Users/home/Projects/crypto-framework\n")
		sb.WriteString("  echo 'VIRGA_AUTH_SECRET=your-secret-here' >> .env\n")
		sb.WriteString("\nOr set CRYPTOFRAMEWORK_API_SECRET environment variable.\n")
		return fmt.Errorf("api_secret / VIRGA_AUTH_SECRET is required for wait_sessions")
	}

	sb.WriteString("Waiting for beacon to establish sessions...\n")
	fmt.Fprintf(sb, "Admin URL: %s\n", adminURL)

	// Login to get JWT token
	loginCmd := fmt.Sprintf(
		`curl -sf -X POST -H "Content-Type: application/json" -d '{"secret":"%s"}' %s/api/login 2>/dev/null`,
		apiSecret, adminURL,
	)

	// Wait up to 5 minutes for sessions to appear
	maxWait := 5 * time.Minute
	pollInterval := 10 * time.Second
	startTime := time.Now()

	for {
		// Check if context is done
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled while waiting for sessions")
		default:
		}

		// Check if we've exceeded max wait time
		if time.Since(startTime) > maxWait {
			sb.WriteString("❌ Timeout: No sessions found after 5 minutes\n")
			return fmt.Errorf("timeout waiting for beacon sessions - no sessions found after %v", maxWait)
		}

		// Try to login and get sessions
		loginOut, err := runScript(ctx, fw, env, loginCmd)
		if err != nil {
			fmt.Fprintf(sb, "⚠️  Login failed (server may not be ready): %v\n", err)
			time.Sleep(pollInterval)
			continue
		}

		// Extract token
		var loginResp struct {
			Token string `json:"token"`
		}
		if jsonErr := json.Unmarshal([]byte(loginOut), &loginResp); jsonErr != nil || loginResp.Token == "" {
			sb.WriteString("⚠️  Could not parse login token - retrying...\n")
			time.Sleep(pollInterval)
			continue
		}

		// Check for sessions
		sessionsCmd := fmt.Sprintf(
			`curl -sf -H "Authorization: Bearer %s" %s/api/sessions 2>/dev/null`,
			loginResp.Token, adminURL,
		)
		sessionsOut, err := runScript(ctx, fw, env, sessionsCmd)
		if err != nil {
			fmt.Fprintf(sb, "⚠️  Sessions query failed: %v\n", err)
			time.Sleep(pollInterval)
			continue
		}

		// Parse sessions response
		var sessionsResp struct {
			Success  bool                     `json:"success"`
			Sessions []map[string]interface{} `json:"sessions"`
		}
		if jsonErr := json.Unmarshal([]byte(sessionsOut), &sessionsResp); jsonErr != nil {
			fmt.Fprintf(sb, "⚠️  Could not parse sessions response: %v\n", jsonErr)
			time.Sleep(pollInterval)
			continue
		}

		if !sessionsResp.Success {
			sb.WriteString("⚠️  Sessions API returned success=false - retrying...\n")
			time.Sleep(pollInterval)
			continue
		}

		sessionCount := len(sessionsResp.Sessions)
		elapsed := time.Since(startTime).Round(time.Second)

		if sessionCount > 0 {
			fmt.Fprintf(sb, "✅ Found %d session(s) after %v\n", sessionCount, elapsed)
			// Log basic session info
			for i, session := range sessionsResp.Sessions {
				if id, ok := session["id"].(string); ok {
					os := ""
					if osVal, hasOS := session["os"].(string); hasOS {
						os = fmt.Sprintf(" (%s)", osVal)
					}
					fmt.Fprintf(sb, "  Session %d: %s%s\n", i+1, id[:8], os)
				}
			}
			return nil
		}

		// Still waiting
		if int(elapsed.Seconds())%30 == 0 { // Log every 30 seconds
			fmt.Fprintf(sb, "⏳ Still waiting for sessions... (%v elapsed)\n", elapsed)
		}

		time.Sleep(pollInterval)
	}
}

// runScript executes a shell command in the framework directory and returns combined output.
func runScript(ctx context.Context, dir string, env []string, command string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = env

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	return buf.String(), err
}

// utmEnvVal retrieves an env var value from the env slice.
func utmEnvVal(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return ""
}

// expandPath expands environment variables and ~ in a file path.
func expandPath(path string) string {
	// First expand environment variables
	expanded := os.ExpandEnv(path)

	// Handle tilde expansion
	if strings.HasPrefix(expanded, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return expanded // Return as-is if we can't get home dir
		}

		if expanded == "~" {
			return home
		} else if strings.HasPrefix(expanded, "~/") {
			return filepath.Join(home, expanded[2:])
		} else if strings.HasPrefix(expanded, "~") {
			// Handle cases like ~user/path (not supported on all platforms)
			return expanded
		}
	}

	return expanded
}
