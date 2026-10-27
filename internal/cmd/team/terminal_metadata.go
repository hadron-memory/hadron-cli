package team

import (
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/output"
)

// A display transformation must never silently become an action argument.
// Shell quoting is still required when a value passes this test unchanged.
func copyArgumentsUnchanged(values ...string) bool {
	for _, value := range values {
		if output.SafeCell(value) != value {
			return false
		}
	}
	return true
}

func roleDefinitionHint(role, platform string) string {
	if !copyArgumentsUnchanged(role) {
		return "command omitted because the role was altered for display; use `hadron app agent list <app> --json` for the original persona role"
	}
	if platform == "windows" {
		return "create a definition for this persona role; read the original with `hadron app agent list <app> --json`"
	}
	return "create one with `hadron team role create " + shellQuote(role) + "`"
}

func writeSwitchoverApplyHint(w io.Writer, app, server, proof, platform string) error {
	if !copyArgumentsUnchanged(app, server, proof) {
		_, err := fmt.Fprintf(w, "Proof: %s — apply command omitted because an argument was altered for display; use --json for the original proof and keep the original app/server arguments.\n", output.SafeCell(proof))
		return err
	}
	if platform == "windows" {
		_, err := fmt.Fprintf(w, "Apply with --app %q, --server %q, and --proof %q.\n", app, server, proof)
		return err
	}
	_, err := fmt.Fprintf(w, "  hadron team attention switchover apply --app %s --server %s --proof %s\n", shellQuote(app), shellQuote(server), shellQuote(proof))
	return err
}

func writeHostCursorHint(w io.Writer, cursor, platform string) error {
	if !copyArgumentsUnchanged(cursor) {
		_, err := fmt.Fprintf(w, "more follow — cursor: %s; --after suggestion omitted because the cursor was altered for display; use --json for the original cursor.\n", output.SafeCell(cursor))
		return err
	}
	if platform == "windows" {
		_, err := fmt.Fprintf(w, "more follow — cursor %q; pass the original cursor as --after.\n", cursor)
		return err
	}
	_, err := fmt.Fprintf(w, "more follow — continue with --after %s\n", shellQuote(cursor))
	return err
}
