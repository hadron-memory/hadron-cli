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
					if !strings.Contains(stderr, "server returned no usage events result") {
						t.Errorf("stderr does not name the missing result: %q", stderr)
					}
					if strings.TrimSpace(out) != "" {
						t.Errorf("stdout must stay empty on the refusal: %q", out)
					}
				})
			}
		}
	}
}
