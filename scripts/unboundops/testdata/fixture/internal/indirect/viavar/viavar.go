// Package viavar calls an operation through a variable.
package viavar

import "github.com/hadron-memory/hadron-cli/internal/api/gen"

func fetch() error {
	call := gen.Agents
	return call(nil)
}

var _ = fetch
