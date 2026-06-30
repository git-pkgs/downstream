package main

import (
	"os"

	"github.com/git-pkgs/downstream/cmd"
)

func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
