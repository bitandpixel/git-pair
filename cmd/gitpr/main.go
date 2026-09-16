// Command gitpr is a local-first peer-review orchestrator for human and
// coding-agent pairs. See PRD.md for the product requirements and README.md for
// the workflow.
package main

import (
	"os"

	"gitpr/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}
