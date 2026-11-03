package team

import "github.com/hadron-memory/hadron-cli/internal/exitcode"

// Syntax only; the server owns matching and requirement validity.
func validateTaskMinTier(tier string) error {
	switch tier {
	case "low", "mid", "high":
		return nil
	default:
		return exitcode.Newf(exitcode.Usage, "--min-tier takes low, mid or high, got %q", tier)
	}
}
