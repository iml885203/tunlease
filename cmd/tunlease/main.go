package main

import (
	"os"

	"github.com/iml885203/tunlease/internal/cliapp"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	if e := cliapp.NewCommandWithVersion(version, buildTime).Execute(); e != nil {
		cliapp.PrintError(os.Stderr, e)
		os.Exit(1)
	}
}
