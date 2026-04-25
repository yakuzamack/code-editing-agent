package main

import (
	"fmt"
	"os"

	"github.com/promacanthus/code-editing-agent/pkg/app"
)

func main() {
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
