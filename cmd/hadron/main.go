package main

import (
	"os"

	"github.com/hadron-memory/hadron-cli/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
