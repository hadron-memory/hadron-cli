package orca

import (
	"errors"
	"os"
)

func openHostReadOnly(string) (*os.File, error) {
	return nil, errors.New("host planner file reads are unsupported on Windows")
}
