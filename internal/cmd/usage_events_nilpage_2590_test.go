package cmd

import (
	"strings"
	"testing"
)

// cli#2590: a usageEventsPage reply with no page (empty data, or an explicit
// null) must exit 1 naming the missing result, on both the plain and the
// --app-key branch, in human and --json mode, never panic.
func TestUsageEventsNoPageExitsNamingIt2590(t *testing.T) {
	replies := map[string]string{
		"empty data": `{"data":{}}`,
		"null page":  `{"data":{"usageEventsPage":null}}`,
	}
	branches := map[string][]string{
		"plain":   {"usage", "events"},
		"app-key": {"usage", "events", "--app-key", "key-1"},
	}
	for rn, reply := range replies {
		for bn, base := range branches {
			for _, mode := range []string{"human", "json"} {
				t.Run(rn+"/"+bn+"/"+mode, func(t *testing.T) {
					args := append([]string{}, base...)
					if mode == "json" {
						args = append(args, "--json")
					}
					out, stderr, code, _ := usage65Run(t, reply, args...)
					if code != 1 {
						t.Fatalf("exit %d, want 1; out=%q stderr=%q", code, out, stderr)
					}
					// Human mode: the message on stderr, stdout empty. --json: the
					// error envelope on stdout is the contract, stderr empty.
					want, other := stderr, out
					if mode == "json" {
						want, other = out, stderr
					}
					if !strings.Contains(want, "server returned no usage events result") {
						t.Errorf("does not name the missing result: %q", want)
					}
					if mode == "human" && strings.TrimSpace(other) != "" {
						t.Errorf("stdout must stay empty on the refusal: %q", other)
					}
					if mode == "json" && strings.TrimSpace(other) != "" {
						t.Errorf("stderr must stay empty in --json: %q", other)
					}
				})
			}
		}
	}
}
