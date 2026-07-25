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
	command := cliapp.NewCommandWithVersion(version, buildTime)
	executed, e := command.ExecuteC()
	if e != nil {
		cliapp.PrintCommandError(os.Stderr, executed, os.Args[1:], e)
		os.Exit(1)
	}
}
