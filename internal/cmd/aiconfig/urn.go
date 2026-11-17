package aiconfig

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// urnConfigDTO is the --json shape of `ai-config get` and `list --with-endpoints`:
// the endpoint-aware config plus the server-computed URN. urn is null when the
// server returns none (a name that cannot be a URN atom, or a server without the
// field). The CLI shows the URN as given; it never builds or checks its shape.
type urnConfigDTO struct {
	endpointConfigDTO
	URN *string `json:"urn"`
}

// urnStyleConfigDTO is the --json item shape of the owner-management list.
type urnStyleConfigDTO struct {
	styleConfigDTO
	URN *string `json:"urn"`
}

// urnMaskedDTO is the --json shape of the default `ai-config list`.
type urnMaskedDTO struct {
	aiConfigDTO
	URN *string `json:"urn"`
}

// isMissingURNField reports whether err is an older server rejecting the urn
// selection and nothing else, so the caller may retry without it. Every server
// message must be the missing-urn shape (a repeat of the same message still
// qualifies): a missing urn beside any other validation message is a refusal to
// report as sent, not a probe answer, and retrying it would discard that
// refusal (#2314).
func isMissingURNField(err error) bool {
	messages := api.ServerMessages(err)
	if len(messages) == 0 {
		return false
	}
	for _, message := range messages {
		if !strings.Contains(message, `"urn"`) || !isMissingSchemaField(message) {
			return false
		}
	}
	return true
}

// notFoundConfig is the one answer for a null aiServiceConfig. A URN the caller
// cannot read comes back null from the server exactly as a missing one does, so
// the message must not distinguish them (cor:api:120:01).
func notFoundConfig(ref string) error {
	return exitcode.Newf(exitcode.NotFound, "AI service config %q was not found", ref)
}

// writeURNLine prints the URN, and nothing for a null one.
func writeURNLine(w io.Writer, urn *string) error {
	if urn == nil || *urn == "" {
		return nil
	}
	_, err := fmt.Fprintf(w, "  urn %s\n", output.SafeCell(*urn))
	return err
}

func hasURN(urn *string) bool { return urn != nil && *urn != "" }

// urnCell is a table cell for a URN: empty for a null one.
func urnCell(urn *string) string {
	if !hasURN(urn) {
		return ""
	}
	return *urn
}
