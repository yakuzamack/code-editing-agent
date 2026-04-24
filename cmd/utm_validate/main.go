package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/promacanthus/code-editing-agent/pkg/tool"
)

func main() {
	_ = godotenv.Load()

	// Set working directory to crypto-framework
	workDir := "~/Projects/crypto-framework"
	if home, err := os.UserHomeDir(); err == nil {
		workDir = filepath.Join(home, "Projects/crypto-framework")
	}
	err := tool.SetWorkingDir(workDir)
	if err != nil {
		log.Fatalf("Failed to set working directory: %v", err)
	}

	// Create utm_validate input
	input := tool.UTMValidateInput{
		Phase:          "full",
		VMName:         "Windows 2",
		ShareDir:       "~/UTM-crypto-audit",
		ApiSecret:      "crypto-lab-secret-2026",
		SkipPreview:    true,
		TimeoutSeconds: 1800, // 30 minutes for full automation
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		log.Fatalf("Failed to marshal input: %v", err)
	}

	fmt.Println("=== Starting UTM Validate Full Phase ===")
	fmt.Printf("Input: %s\n", string(inputJSON))
	fmt.Println("")
	fmt.Println("📋 IMPORTANT: Before running, ensure UTM VM settings are correct:")
	fmt.Println("   1. Open UTM → Windows 2 → Settings → Sharing")
	fmt.Println("   2. Set Share Directory to: ~/UTM-crypto-audit")
	fmt.Println("   3. Save settings and restart the VM")
	fmt.Println("")

	// Execute utm_validate tool
	result, err := tool.UTMValidate(inputJSON)
	if err != nil {
		log.Fatalf("UTM Validate failed: %v", err)
	}

	fmt.Println("=== UTM Validate Results ===")
	fmt.Println(result)
}
