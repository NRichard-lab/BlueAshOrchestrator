package main

import (
	"fmt"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
	"github.com/aoagents/agent-orchestrator/backend/internal/cli"
)

func main() {
	// Before anything reads the environment or starts a child: drop another
	// product's daemon coordinates (see brand.ScrubForeignEnv).
	brand.ScrubForeignEnv()
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
}
