package orca

import (
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"os"
)

func openHostReadOnly(string) (*os.File, error) {
	return nil, exitcode.Newf(exitcode.Usage, "orca host plan file inspection is unavailable on Windows")
}
