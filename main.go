package main

import (
	"os"

	"github.com/apsdsm/meimei/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
