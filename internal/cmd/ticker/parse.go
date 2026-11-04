package ticker

import (
	"encoding/json"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ParseQuietHours validates the JSON-object boundary; the server validates its
// timezone and windows. Shared by ticker and Agent ticker-template commands.
func ParseQuietHours(value string) (*json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &obj); err != nil || obj == nil {
		return nil, exitcode.Newf(exitcode.Usage, "--quiet-hours takes a JSON object")
	}
	raw := json.RawMessage(value)
	return &raw, nil
}

// ParseRelevance is the common ticker/template flag vocabulary.
func ParseRelevance(value string) (gen.TickerRelevance, error) {
	r := gen.TickerRelevance(strings.ToUpper(value))
	if r != gen.TickerRelevanceAll && r != gen.TickerRelevanceMentions {
		return "", exitcode.Newf(exitcode.Usage, "--relevance takes all or mentions")
	}
	return r, nil
}

// ParseReasons preserves the ticker command's canonical order and aliases.
func ParseReasons(value string) ([]gen.TickerReason, error) {
	seen := map[string]bool{}
	for _, r := range strings.Split(value, ",") {
		r = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(r)), "_", "-")
		if r == "" {
			return nil, exitcode.Newf(exitcode.Usage, "reasons must name at least one reason: a ticker with none would never fire.")
		}
		if r != "read-team-channel" && r != "every-tick" && r != "live-check" {
			return nil, exitcode.Newf(exitcode.Usage, "reasons are from: read-team-channel, every-tick, live-check.")
		}
		seen[r] = true
	}
	out := []gen.TickerReason{}
	for _, r := range []string{"read-team-channel", "every-tick", "live-check"} {
		if seen[r] {
			out = append(out, gen.TickerReason(strings.ReplaceAll(strings.ToUpper(r), "-", "_")))
		}
	}
	return out, nil
}
