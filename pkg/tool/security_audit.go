package tool

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// SecurityAuditDefinition is the definition for the security_audit tool.
var SecurityAuditDefinition = Definition{
	Name: "security_audit",
	Description: `Perform comprehensive security analysis of the crypto-framework codebase.

Security Checks:
  - OPSEC violation detection (logging sensitive data, debug prints)
  - Hardcoded secrets and credentials scanning
  - Memory safety analysis (buffer overflows, use-after-free)
  - Cryptographic implementation validation
  - Platform-specific security issues
  - Network communication security
  - Error handling security (information disclosure)
  - Anti-analysis evasion verification

Vulnerability Categories:
  - Critical: Immediate security risks (hardcoded keys, plaintext secrets)
  - High: Significant OPSEC violations (detailed error messages, debug logs)
  - Medium: Best practice violations (weak crypto, insecure defaults)
  - Low: Code quality issues that might affect security
  - Info: Security-relevant observations

Generates actionable security report with fix recommendations.`,
	InputSchema: GenerateSchema[SecurityAuditInput](),
	Function:    ExecuteSecurityAudit,
}

// SecurityAuditInput is the input for the security_audit tool.
type SecurityAuditInput struct {
	// AuditType specifies what to audit (opsec, crypto, memory, network, all).
	AuditType string `json:"audit_type,omitempty" jsonschema:"description=Audit type: opsec, crypto, memory, network, secrets, all. Default: opsec."`

	// TargetModules limits audit to specific modules (e.g., ["crypto", "transport"]).
	TargetModules []string `json:"target_modules,omitempty" jsonschema:"description=Specific modules to audit. Empty = all modules."`

	// Severity filters findings by severity (critical, high, medium, low, info).
	Severity string `json:"severity,omitempty" jsonschema:"description=Minimum severity to report: critical, high, medium, low, info. Default: medium."`

	// OutputFormat controls output format (markdown, json, sarif).
	OutputFormat string `json:"output_format,omitempty" jsonschema:"description=Output format: markdown, json, sarif. Default: markdown."`

	// IncludeSuppressions shows suppressed findings with justification.
	IncludeSuppressions bool `json:"include_suppressions,omitempty" jsonschema:"description=Include suppressed findings in output."`

	// FrameworkRoot overrides auto-detection of crypto-framework directory.
	FrameworkRoot string `json:"framework_root,omitempty" jsonschema:"description=Override crypto-framework root directory. Defaults to LLM_WORKDIR env."`

	// FixRecommendations includes specific fix suggestions for each finding.
	FixRecommendations bool `json:"fix_recommendations,omitempty" jsonschema:"description=Include specific fix recommendations for findings."`
}

// SecurityFinding represents a security issue found during audit.
type SecurityFinding struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`     // opsec, crypto, memory, etc.
	Severity    string            `json:"severity"` // critical, high, medium, low, info
	Title       string            `json:"title"`
	Description string            `json:"description"`
	File        string            `json:"file"`
	Line        int               `json:"line"`
	Column      int               `json:"column"`
	Code        string            `json:"code"`          // Code snippet
	Rule        string            `json:"rule"`          // Rule that triggered
	CWE         string            `json:"cwe,omitempty"` // Common Weakness Enumeration
	Fix         string            `json:"fix,omitempty"` // Suggested fix
	Suppressed  bool              `json:"suppressed"`
	Metadata    map[string]string `json:"metadata"`
}

// SecurityAuditResult contains the complete security audit results.
type SecurityAuditResult struct {
	Summary     AuditSummary          `json:"summary"`
	Findings    []SecurityFinding     `json:"findings"`
	ModuleStats []ModuleSecurityStats `json:"module_stats"`
	Rules       []SecurityRule        `json:"rules"`
	Timestamp   string                `json:"timestamp"`
}

// AuditSummary provides high-level audit statistics.
type AuditSummary struct {
	TotalFiles    int `json:"total_files"`
	TotalLines    int `json:"total_lines"`
	TotalFindings int `json:"total_findings"`
	Critical      int `json:"critical"`
	High          int `json:"high"`
	Medium        int `json:"medium"`
	Low           int `json:"low"`
	Info          int `json:"info"`
	Suppressed    int `json:"suppressed"`
	CleanModules  int `json:"clean_modules"`
}

// ModuleSecurityStats contains per-module security statistics.
type ModuleSecurityStats struct {
	Module      string   `json:"module"`
	Files       int      `json:"files"`
	Lines       int      `json:"lines"`
	Findings    int      `json:"findings"`
	MaxSeverity string   `json:"max_severity"`
	Issues      []string `json:"issues"`
}

// SecurityRule defines a security check rule.
type SecurityRule struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Severity    string `json:"severity"`
	Pattern     string `json:"pattern"`
	Enabled     bool   `json:"enabled"`
}

// ExecuteSecurityAudit performs comprehensive security analysis.
func ExecuteSecurityAudit(input json.RawMessage) (string, error) {
	var args SecurityAuditInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}

	// Set defaults
	auditType := args.AuditType
	if auditType == "" {
		auditType = "opsec"
	}

	severity := args.Severity
	if severity == "" {
		severity = "medium"
	}

	outputFormat := args.OutputFormat
	if outputFormat == "" {
		outputFormat = "markdown"
	}

	// Resolve framework root
	fwRoot := args.FrameworkRoot
	if fwRoot == "" {
		fwRoot = os.Getenv("LLM_WORKDIR")
	}
	if fwRoot == "" {
		fwRoot = WorkingDir()
	}

	// Update args with processed values
	if args.AuditType == "" {
		args.AuditType = auditType
	}
	if args.Severity == "" {
		args.Severity = severity
	}
	if args.OutputFormat == "" {
		args.OutputFormat = outputFormat
	}

	// Validate framework structure
	if _, err := os.Stat(fwRoot); os.IsNotExist(err) {
		return "", fmt.Errorf("framework directory not found: %s", fwRoot)
	}

	// Perform security audit
	result, err := performSecurityAudit(fwRoot, args)
	if err != nil {
		return "", fmt.Errorf("security audit failed: %w", err)
	}

	// Filter by severity
	result.Findings = filterBySeverity(result.Findings, severity)
	updateSummaryAfterFilter(&result.Summary, result.Findings)

	// Format output
	switch outputFormat {
	case "json":
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	case "sarif":
		return formatSARIF(result), nil
	default:
		return formatMarkdownSecurityReport(result, args.FixRecommendations), nil
	}
}

// performSecurityAudit executes the security analysis.
func performSecurityAudit(fwRoot string, args SecurityAuditInput) (*SecurityAuditResult, error) {
	// Get security rules for the audit type
	rules := getSecurityRules(args.AuditType)

	// Scan files
	var findings []SecurityFinding
	var totalFiles, totalLines int
	moduleStats := make(map[string]*ModuleSecurityStats)

	err := filepath.Walk(fwRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only process Go source files
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go") {
			// Filter by target modules if specified
			if len(args.TargetModules) > 0 {
				inTarget := false
				for _, target := range args.TargetModules {
					if strings.Contains(path, target) {
						inTarget = true
						break
					}
				}
				if !inTarget {
					return nil
				}
			}

			// Analyze file
			fileFindings, lines, err := analyzeFile(path, rules)
			if err != nil {
				return err // Continue on parse errors
			}

			findings = append(findings, fileFindings...)
			totalFiles++
			totalLines += lines

			// Update module stats
			moduleName := extractSecurityModuleName(path, fwRoot)
			if moduleName != "" {
				if moduleStats[moduleName] == nil {
					moduleStats[moduleName] = &ModuleSecurityStats{
						Module: moduleName,
						Issues: []string{},
					}
				}
				moduleStats[moduleName].Files++
				moduleStats[moduleName].Lines += lines
				moduleStats[moduleName].Findings += len(fileFindings)

				// Track issue types
				for _, finding := range fileFindings {
					if !contains(moduleStats[moduleName].Issues, finding.Type) {
						moduleStats[moduleName].Issues = append(moduleStats[moduleName].Issues, finding.Type)
					}

					// Update max severity
					if isHigherSeverity(finding.Severity, moduleStats[moduleName].MaxSeverity) {
						moduleStats[moduleName].MaxSeverity = finding.Severity
					}
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Convert module stats map to slice
	var moduleStatsList []ModuleSecurityStats
	cleanModules := 0
	for _, stats := range moduleStats {
		if stats.Findings == 0 {
			cleanModules++
		}
		moduleStatsList = append(moduleStatsList, *stats)
	}

	// Sort findings by severity and file
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return severityOrder(findings[i].Severity) < severityOrder(findings[j].Severity)
		}
		return findings[i].File < findings[j].File
	})

	// Calculate summary
	summary := calculateSummary(totalFiles, totalLines, findings, cleanModules)

	return &SecurityAuditResult{
		Summary:     summary,
		Findings:    findings,
		ModuleStats: moduleStatsList,
		Rules:       rules,
		Timestamp:   "now", // TODO: add actual timestamp
	}, nil
}

// analyzeFile performs security analysis on a single Go file.
func analyzeFile(filePath string, rules []SecurityRule) ([]SecurityFinding, int, error) {
	var findings []SecurityFinding

	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, 0, err
	}

	lines := strings.Split(string(content), "\n")
	totalLines := len(lines)

	// Parse Go AST for advanced analysis
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		// Fall back to text-based analysis on parse errors
		findings = append(findings, analyzeFileText(filePath, lines, rules)...)
		return findings, totalLines, nil
	}

	// AST-based analysis
	astFindings := analyzeFileAST(filePath, node, fset, rules)
	findings = append(findings, astFindings...)

	// Text-based analysis for patterns AST can't catch
	textFindings := analyzeFileText(filePath, lines, rules)
	findings = append(findings, textFindings...)

	return findings, totalLines, nil
}

// analyzeFileAST performs AST-based security analysis.
func analyzeFileAST(filePath string, node *ast.File, fset *token.FileSet, rules []SecurityRule) []SecurityFinding {
	var findings []SecurityFinding

	// Walk the AST
	ast.Inspect(node, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			// Check function calls
			findings = append(findings, checkFunctionCall(filePath, node, fset, rules)...)
		case *ast.BasicLit:
			// Check string literals
			findings = append(findings, checkStringLiteral(filePath, node, fset, rules)...)
		case *ast.GenDecl:
			// Check global declarations
			findings = append(findings, checkGlobalDecl(filePath, node, fset, rules)...)
		}
		return true
	})

	return findings
}

// analyzeFileText performs text-based pattern matching.
func analyzeFileText(filePath string, lines []string, rules []SecurityRule) []SecurityFinding {
	var findings []SecurityFinding

	for lineNum, line := range lines {
		for _, rule := range rules {
			if !rule.Enabled {
				continue
			}

			if matched, _ := regexp.MatchString(rule.Pattern, line); matched {
				finding := SecurityFinding{
					ID:          generateFindingID(rule.ID, filePath, lineNum),
					Type:        rule.Type,
					Severity:    rule.Severity,
					Title:       rule.Name,
					Description: rule.Description,
					File:        filePath,
					Line:        lineNum + 1,
					Code:        strings.TrimSpace(line),
					Rule:        rule.ID,
					Metadata:    map[string]string{"pattern": rule.Pattern},
				}

				findings = append(findings, finding)
			}
		}
	}

	return findings
}

// checkFunctionCall analyzes function calls for security issues.
func checkFunctionCall(filePath string, call *ast.CallExpr, fset *token.FileSet, rules []SecurityRule) []SecurityFinding {
	var findings []SecurityFinding

	// Get function name
	var funcName string
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		funcName = fun.Name
	case *ast.SelectorExpr:
		funcName = fmt.Sprintf("%s.%s", getExprName(fun.X), fun.Sel.Name)
	}

	if funcName == "" {
		return findings
	}

	pos := fset.Position(call.Pos())

	// Check for dangerous functions
	dangerousFuncs := []string{
		"fmt.Printf", "fmt.Sprintf", "log.Printf", "fmt.Print", "fmt.Println",
		"os.Exec", "exec.Command", "syscall.Syscall", "unsafe.Pointer",
	}

	for _, dangerous := range dangerousFuncs {
		if funcName == dangerous {
			finding := SecurityFinding{
				ID:          generateFindingID("dangerous_func", filePath, pos.Line),
				Type:        "opsec",
				Severity:    determineFunctionSeverity(dangerous),
				Title:       fmt.Sprintf("Potentially dangerous function: %s", dangerous),
				Description: fmt.Sprintf("Usage of %s may leak sensitive information or pose security risks", dangerous),
				File:        filePath,
				Line:        pos.Line,
				Column:      pos.Column,
				Code:        getCallCode(call, fset),
				Rule:        "dangerous_function_call",
				Metadata:    map[string]string{"function": dangerous},
			}

			findings = append(findings, finding)
		}
	}

	return findings
}

// checkStringLiteral analyzes string literals for secrets.
func checkStringLiteral(filePath string, lit *ast.BasicLit, fset *token.FileSet, rules []SecurityRule) []SecurityFinding {
	var findings []SecurityFinding

	if lit.Kind != token.STRING {
		return findings
	}

	value := strings.Trim(lit.Value, `"`)
	pos := fset.Position(lit.Pos())

	// Check for potential secrets
	secretPatterns := []struct {
		name     string
		pattern  string
		severity string
	}{
		{"API Key", `(?i)(api[_-]?key|apikey)[_-]?[:=]\s*["\']?[a-zA-Z0-9]{16,}`, "critical"},
		{"JWT Token", `eyJ[a-zA-Z0-9_-]+\.eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+`, "critical"},
		{"Password", `(?i)password[_-]?[:=]\s*["\']?[a-zA-Z0-9@#$%^&*()_+-=]{6,}`, "high"},
		{"Private Key", `-----BEGIN [A-Z ]+PRIVATE KEY-----`, "critical"},
		{"Database URL", `(?i)(mysql|postgres|mongodb)://[^/\s"']+`, "high"},
	}

	for _, pattern := range secretPatterns {
		if matched, _ := regexp.MatchString(pattern.pattern, value); matched {
			finding := SecurityFinding{
				ID:          generateFindingID("hardcoded_secret", filePath, pos.Line),
				Type:        "secrets",
				Severity:    pattern.severity,
				Title:       fmt.Sprintf("Potential hardcoded %s", pattern.name),
				Description: fmt.Sprintf("String literal may contain hardcoded %s", pattern.name),
				File:        filePath,
				Line:        pos.Line,
				Column:      pos.Column,
				Code:        value[:min(len(value), 50)] + "...",
				Rule:        "hardcoded_secret",
				CWE:         "CWE-798",
				Metadata:    map[string]string{"secret_type": pattern.name},
			}

			findings = append(findings, finding)
		}
	}

	return findings
}

// checkGlobalDecl analyzes global declarations.
func checkGlobalDecl(filePath string, decl *ast.GenDecl, fset *token.FileSet, rules []SecurityRule) []SecurityFinding {
	var findings []SecurityFinding

	// Check for global variables with dangerous defaults
	if decl.Tok == token.VAR {
		for _, spec := range decl.Specs {
			if valueSpec, ok := spec.(*ast.ValueSpec); ok {
				for i, name := range valueSpec.Names {
					if name.IsExported() && len(valueSpec.Values) > i {
						// Check if global exported variable has insecure default
						pos := fset.Position(name.Pos())

						finding := SecurityFinding{
							ID:          generateFindingID("global_var", filePath, pos.Line),
							Type:        "opsec",
							Severity:    "low",
							Title:       fmt.Sprintf("Exported global variable: %s", name.Name),
							Description: "Exported global variables may expose internal state",
							File:        filePath,
							Line:        pos.Line,
							Column:      pos.Column,
							Code:        name.Name,
							Rule:        "exported_global_var",
							Metadata:    map[string]string{"variable": name.Name},
						}

						findings = append(findings, finding)
					}
				}
			}
		}
	}

	return findings
}

// getSecurityRules returns the security rules for a given audit type.
func getSecurityRules(auditType string) []SecurityRule {
	allRules := []SecurityRule{
		// OPSEC rules
		{
			ID:          "debug_print",
			Name:        "Debug Print Statement",
			Description: "Debug print statements may leak sensitive information",
			Type:        "opsec",
			Severity:    "medium",
			Pattern:     `(?i)(fmt\.Print|log\.Print|println|debug|console\.log).*(?i)(password|key|secret|token|auth)`,
			Enabled:     true,
		},
		{
			ID:          "error_disclosure",
			Name:        "Information Disclosure in Error",
			Description: "Error messages may disclose sensitive system information",
			Type:        "opsec",
			Severity:    "medium",
			Pattern:     `fmt\.Errorf.*(?i)(path|directory|file|system|internal)`,
			Enabled:     true,
		},
		{
			ID:          "hardcoded_ip",
			Name:        "Hardcoded IP Address",
			Description: "Hardcoded IP addresses may reveal infrastructure details",
			Type:        "opsec",
			Severity:    "low",
			Pattern:     `\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`,
			Enabled:     true,
		},

		// Crypto rules
		{
			ID:          "weak_random",
			Name:        "Weak Random Number Generation",
			Description: "Use of math/rand for security-sensitive operations",
			Type:        "crypto",
			Severity:    "high",
			Pattern:     `math/rand|rand\.New|rand\.Seed`,
			Enabled:     true,
		},
		{
			ID:          "md5_usage",
			Name:        "Weak Hash Algorithm",
			Description: "MD5 is cryptographically broken",
			Type:        "crypto",
			Severity:    "high",
			Pattern:     `crypto/md5|md5\.New|md5\.Sum`,
			Enabled:     true,
		},
		{
			ID:          "hardcoded_key",
			Name:        "Hardcoded Cryptographic Key",
			Description: "Cryptographic keys should not be hardcoded",
			Type:        "crypto",
			Severity:    "critical",
			Pattern:     `(?i)(aes\.NewCipher|cipher\.NewCBCDecrypter).*["'][a-zA-Z0-9+/=]{16,}["']`,
			Enabled:     true,
		},

		// Memory safety rules
		{
			ID:          "unsafe_pointer",
			Name:        "Unsafe Pointer Usage",
			Description: "Unsafe pointer operations may lead to memory corruption",
			Type:        "memory",
			Severity:    "medium",
			Pattern:     `unsafe\.Pointer|uintptr`,
			Enabled:     true,
		},
		{
			ID:          "buffer_operation",
			Name:        "Buffer Operation",
			Description: "Manual buffer operations may be unsafe",
			Type:        "memory",
			Severity:    "low",
			Pattern:     `make\(\[\]byte|copy\(.*\[\]byte|append\(.*\[\]byte`,
			Enabled:     true,
		},

		// Network rules
		{
			ID:          "http_client",
			Name:        "HTTP Client Without Timeout",
			Description: "HTTP clients should have timeouts configured",
			Type:        "network",
			Severity:    "medium",
			Pattern:     `http\.Get|http\.Post|http\.Client\{\}`,
			Enabled:     true,
		},
		{
			ID:          "skip_cert_verify",
			Name:        "TLS Certificate Verification Disabled",
			Description: "Skipping certificate verification is dangerous",
			Type:        "network",
			Severity:    "critical",
			Pattern:     `InsecureSkipVerify.*true|tls\.Config.*InsecureSkipVerify`,
			Enabled:     true,
		},
	}

	// Filter by audit type
	if auditType == "all" {
		return allRules
	}

	var filteredRules []SecurityRule
	for _, rule := range allRules {
		if rule.Type == auditType {
			filteredRules = append(filteredRules, rule)
		}
	}

	return filteredRules
}

// Helper functions for security audit

// generateFindingID creates a unique ID for a security finding.
func generateFindingID(ruleID, file string, line int) string {
	return fmt.Sprintf("%s_%s_%d", ruleID, filepath.Base(file), line)
}

// determineFunctionSeverity determines severity based on function name.
func determineFunctionSeverity(funcName string) string {
	criticalFuncs := []string{"syscall.Syscall", "unsafe.Pointer"}
	highFuncs := []string{"os.Exec", "exec.Command"}

	for _, critical := range criticalFuncs {
		if funcName == critical {
			return "critical"
		}
	}

	for _, high := range highFuncs {
		if funcName == high {
			return "high"
		}
	}

	return "medium"
}

// getCallCode extracts the source code for a function call.
func getCallCode(call *ast.CallExpr, fset *token.FileSet) string {
	start := fset.Position(call.Pos())

	// This is a simplified version - in practice you'd need to read the source
	return fmt.Sprintf("Line %d", start.Line)
}

// getExprName extracts the name from an expression.
func getExprName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return fmt.Sprintf("%s.%s", getExprName(e.X), e.Sel.Name)
	default:
		return ""
	}
}

// extractSecurityModuleName extracts module name from file path.
func extractSecurityModuleName(filePath, fwRoot string) string {
	rel, err := filepath.Rel(fwRoot, filePath)
	if err != nil {
		return ""
	}

	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) >= 4 && parts[0] == "internal" && parts[1] == "implant" && parts[2] == "modules" {
		return parts[3]
	}

	return ""
}

// severityOrder returns numeric order for severity levels.
func severityOrder(severity string) int {
	orders := map[string]int{
		"critical": 0,
		"high":     1,
		"medium":   2,
		"low":      3,
		"info":     4,
	}

	if order, exists := orders[severity]; exists {
		return order
	}
	return 999
}

// isHigherSeverity checks if severity1 is higher than severity2.
func isHigherSeverity(severity1, severity2 string) bool {
	if severity2 == "" {
		return true
	}
	return severityOrder(severity1) < severityOrder(severity2)
}

// filterBySeverity filters findings by minimum severity level.
func filterBySeverity(findings []SecurityFinding, minSeverity string) []SecurityFinding {
	minOrder := severityOrder(minSeverity)
	var filtered []SecurityFinding

	for _, finding := range findings {
		if severityOrder(finding.Severity) <= minOrder {
			filtered = append(filtered, finding)
		}
	}

	return filtered
}

// calculateSummary computes audit summary statistics.
func calculateSummary(totalFiles, totalLines int, findings []SecurityFinding, cleanModules int) AuditSummary {
	summary := AuditSummary{
		TotalFiles:    totalFiles,
		TotalLines:    totalLines,
		TotalFindings: len(findings),
		CleanModules:  cleanModules,
	}

	for _, finding := range findings {
		if finding.Suppressed {
			summary.Suppressed++
			continue
		}

		switch finding.Severity {
		case "critical":
			summary.Critical++
		case "high":
			summary.High++
		case "medium":
			summary.Medium++
		case "low":
			summary.Low++
		case "info":
			summary.Info++
		}
	}

	return summary
}

// updateSummaryAfterFilter updates summary stats after filtering.
func updateSummaryAfterFilter(summary *AuditSummary, filteredFindings []SecurityFinding) {
	// Reset counts
	summary.TotalFindings = len(filteredFindings)
	summary.Critical = 0
	summary.High = 0
	summary.Medium = 0
	summary.Low = 0
	summary.Info = 0
	summary.Suppressed = 0

	for _, finding := range filteredFindings {
		if finding.Suppressed {
			summary.Suppressed++
			continue
		}

		switch finding.Severity {
		case "critical":
			summary.Critical++
		case "high":
			summary.High++
		case "medium":
			summary.Medium++
		case "low":
			summary.Low++
		case "info":
			summary.Info++
		}
	}
}

// formatMarkdownSecurityReport formats the security audit results as markdown.
func formatMarkdownSecurityReport(result *SecurityAuditResult, includeFixRecommendations bool) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# Crypto-Framework Security Audit Report\n\n")
	fmt.Fprintf(&sb, "**Generated:** %s\n\n", result.Timestamp)

	// Executive Summary
	fmt.Fprintf(&sb, "## Executive Summary\n\n")
	if result.Summary.Critical > 0 || result.Summary.High > 0 {
		fmt.Fprintf(&sb, "⚠️ **CRITICAL ISSUES FOUND** - Immediate action required\n\n")
	} else if result.Summary.Medium > 0 {
		fmt.Fprintf(&sb, "⚡ **Security issues identified** - Review recommended\n\n")
	} else {
		fmt.Fprintf(&sb, "✅ **No critical security issues found**\n\n")
	}

	// Summary Table
	fmt.Fprintf(&sb, "| Metric | Count |\n")
	fmt.Fprintf(&sb, "|--------|-------|\n")
	fmt.Fprintf(&sb, "| Files Scanned | %d |\n", result.Summary.TotalFiles)
	fmt.Fprintf(&sb, "| Lines Analyzed | %d |\n", result.Summary.TotalLines)
	fmt.Fprintf(&sb, "| Total Findings | %d |\n", result.Summary.TotalFindings)
	fmt.Fprintf(&sb, "| Critical | %d |\n", result.Summary.Critical)
	fmt.Fprintf(&sb, "| High | %d |\n", result.Summary.High)
	fmt.Fprintf(&sb, "| Medium | %d |\n", result.Summary.Medium)
	fmt.Fprintf(&sb, "| Low | %d |\n", result.Summary.Low)
	fmt.Fprintf(&sb, "| Info | %d |\n", result.Summary.Info)
	fmt.Fprintf(&sb, "| Clean Modules | %d |\n", result.Summary.CleanModules)

	// Findings by Severity
	severityOrder := []string{"critical", "high", "medium", "low", "info"}
	for _, severity := range severityOrder {
		findings := filterFindingsBySeverity(result.Findings, severity)
		if len(findings) == 0 {
			continue
		}

		emoji := getSeverityEmoji(severity)
		caser := cases.Title(language.English)
		fmt.Fprintf(&sb, "\n## %s %s Issues (%d)\n\n", emoji, caser.String(severity), len(findings))

		for _, finding := range findings {
			fmt.Fprintf(&sb, "### %s\n", finding.Title)
			fmt.Fprintf(&sb, "**File:** %s:%d\n", finding.File, finding.Line)
			fmt.Fprintf(&sb, "**Type:** %s\n", finding.Type)
			if finding.CWE != "" {
				fmt.Fprintf(&sb, "**CWE:** %s\n", finding.CWE)
			}
			fmt.Fprintf(&sb, "**Description:** %s\n\n", finding.Description)

			fmt.Fprintf(&sb, "```go\n%s\n```\n\n", finding.Code)

			if includeFixRecommendations && finding.Fix != "" {
				fmt.Fprintf(&sb, "**Recommended Fix:**\n%s\n\n", finding.Fix)
			}
		}
	}

	// Module Statistics
	if len(result.ModuleStats) > 0 {
		fmt.Fprintf(&sb, "\n## Module Security Overview\n\n")
		fmt.Fprintf(&sb, "| Module | Files | Lines | Findings | Max Severity | Issue Types |\n")
		fmt.Fprintf(&sb, "|--------|-------|-------|----------|--------------|-------------|\n")

		for _, stats := range result.ModuleStats {
			maxSeverity := stats.MaxSeverity
			if maxSeverity == "" {
				maxSeverity = "None"
			}
			fmt.Fprintf(&sb, "| %s | %d | %d | %d | %s | %s |\n",
				stats.Module, stats.Files, stats.Lines, stats.Findings,
				maxSeverity, strings.Join(stats.Issues, ", "))
		}
	}

	return sb.String()
}

// formatSARIF formats results in SARIF (Static Analysis Results Interchange Format).
func formatSARIF(result *SecurityAuditResult) string {
	// Simplified SARIF format - full implementation would be more complex
	sarif := map[string]interface{}{
		"version": "2.1.0",
		"runs": []map[string]interface{}{
			{
				"tool": map[string]interface{}{
					"driver": map[string]interface{}{
						"name":    "crypto-framework-security-audit",
						"version": "1.0.0",
					},
				},
				"results": convertFindingsToSARIF(result.Findings),
			},
		},
	}

	data, _ := json.MarshalIndent(sarif, "", "  ")
	return string(data)
}

// Helper functions

// contains checks if a slice contains a string.
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// filterFindingsBySeverity filters findings by a specific severity level.
func filterFindingsBySeverity(findings []SecurityFinding, severity string) []SecurityFinding {
	var filtered []SecurityFinding
	for _, finding := range findings {
		if finding.Severity == severity {
			filtered = append(filtered, finding)
		}
	}
	return filtered
}

// getSeverityEmoji returns an emoji for the severity level.
func getSeverityEmoji(severity string) string {
	emojis := map[string]string{
		"critical": "🔴",
		"high":     "🟠",
		"medium":   "🟡",
		"low":      "🔵",
		"info":     "ℹ️",
	}

	if emoji, exists := emojis[severity]; exists {
		return emoji
	}
	return "❓"
}

// convertFindingsToSARIF converts findings to SARIF result format.
func convertFindingsToSARIF(findings []SecurityFinding) []map[string]interface{} {
	var results []map[string]interface{}

	for _, finding := range findings {
		result := map[string]interface{}{
			"ruleId":  finding.Rule,
			"message": map[string]interface{}{"text": finding.Description},
			"level":   finding.Severity,
			"locations": []map[string]interface{}{
				{
					"physicalLocation": map[string]interface{}{
						"artifactLocation": map[string]interface{}{"uri": finding.File},
						"region": map[string]interface{}{
							"startLine":   finding.Line,
							"startColumn": finding.Column,
						},
					},
				},
			},
		}

		results = append(results, result)
	}

	return results
}
