package cmdutil

import (
	"math"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// PageLimit validates a --limit for a one-page read of the paged lists
// (cor:api:120:04, cli#2032). It returns nil when the flag was not given, so
// the server's default applies. Any positive value through GraphQL Int max is
// forwarded: the server clamps above its cap and never refuses it, so the CLI
// holds no cap of its own. Zero, negatives and overflow are usage errors.
func PageLimit(cmd *cobra.Command, limit int) (*int, error) {
	if !cmd.Flags().Changed("limit") {
		return nil, nil
	}
	if limit < 1 || limit > math.MaxInt32 {
		return nil, exitcode.Newf(exitcode.Usage, "--limit must be a positive whole number (the server clamps it to its page cap)")
	}
	return &limit, nil
}

// PageAfter returns the --after cursor to forward, or nil when it was not
// given. The cursor is opaque: it is passed back exactly as a previous page
// returned it, never decoded, edited or built. An empty one is refused.
func PageAfter(cmd *cobra.Command, after string) (*string, error) {
	if !cmd.Flags().Changed("after") {
		return nil, nil
	}
	if after == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--after cannot be empty; pass the next cursor a previous page printed")
	}
	return &after, nil
}

// CheckPage rejects page metadata that cannot be trusted, so a malformed reply
// is an error and never an empty or partial success: hasMore with no usable
// cursor, or a cursor equal to the one this request sent (the page did not
// advance). It reads the cursor as an opaque string and does not interpret it.
func CheckPage(hasMore bool, nextCursor, sentAfter *string) error {
	if !hasMore {
		return nil
	}
	if nextCursor == nil || *nextCursor == "" {
		return exitcode.Newf(exitcode.Error, "server returned a page with more rows but no next cursor")
	}
	if sentAfter != nil && *nextCursor == *sentAfter {
		return exitcode.Newf(exitcode.Error, "server returned the same cursor it was given; the page did not advance")
	}
	return nil
}
