package main

import (
	"os"

	"github.com/phucvin/terminal-use-01/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
