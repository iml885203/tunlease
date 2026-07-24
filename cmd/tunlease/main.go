package main

import (
	"fmt"
	"os"

	"github.com/iml885203/tunlease/internal/cliapp"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	if e := cliapp.NewCommandWithVersion(version, buildTime).Execute(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
