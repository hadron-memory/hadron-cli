package cmdutil

import (
	"errors"
	"unicode"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// The grant signing request's DTO, its URL-safety check and its error mapping,
// shared by `team grant ...` (a worker asks for a signature) and `secret client
// issue` (a person asks for their own). Nothing here signs or activates a grant
// (cor:acl:140:07: a person signs, on the ceremony page), and the CLI never
// builds or rewrites the ceremony URL.

// ErrNoSigningRequest is a mutation that answered null where it must answer a
// request: a malformed server answer, reported as one rather than dereferenced.
var ErrNoSigningRequest = errors.New("server returned no signing request")

// grantSigningFields are the schema names a grant-signing operation can be
// refused by when the server has no signing surface (its switch off, or older).
var grantSigningFields = []string{
	"createGrant", "CreateGrantInput", "requestGrantSignature", "withdrawGrantSigningRequest",
	"grantSigningRequest", "GrantSigningRequest", "GrantSigningRequestState",
}

// MapGrantSigningError is api.MapErrorKeepingCode, except that a server whose
// every error is a missing signing name gets its own sentence: the generic
// skew advice (upgrade) is wrong where signing is simply not offered. A mixed
// envelope (a missing name beside a real refusal) is not swallowed.
func MapGrantSigningError(err error) error {
	if errors.Is(err, ErrNoSigningRequest) {
		return exitcode.Newf(exitcode.Error, "server returned no signing request")
	}
	if api.IsGraphQLValidationOnlyFor(err, grantSigningFields...) {
		return api.WithServerCode(exitcode.Newf(exitcode.Usage, "this server does not offer grant requests and grant signing"), err)
	}
	return api.MapErrorKeepingCode(err)
}

// GrantSigningRequestDTO is the stable --json shape of a grant signing request.
type GrantSigningRequestDTO struct {
	ID             string                   `json:"id"`
	GrantID        string                   `json:"grantId"`
	Version        int                      `json:"version"`
	TermsHash      string                   `json:"termsHash"`
	PayloadVersion string                   `json:"payloadVersion"`
	State          string                   `json:"state"`
	RequestedBy    GrantSigningRequesterDTO `json:"requestedBy"`
	CreatedAt      string                   `json:"createdAt"`
	ExpiresAt      string                   `json:"expiresAt"`
	EndedAt        *string                  `json:"endedAt"`
	LastRefusedAt  *string                  `json:"lastRefusedAt"` // latest observed refused signing attempt; null = none observed (cli#2727)
	CeremonyURL    string                   `json:"ceremonyUrl"`
}

// GrantSigningRequesterDTO names who asked for the signature.
type GrantSigningRequesterDTO struct {
	UserID    *string `json:"userId"`
	AppID     *string `json:"appId"`
	WorkerID  *string `json:"workerId"`
	SessionID *string `json:"sessionId"`
}

// GrantSigningRequestFrom copies the generated fields into the stable DTO.
func GrantSigningRequestFrom(r *gen.TeamGrantSigningRequestFields) GrantSigningRequestDTO {
	d := GrantSigningRequestDTO{
		ID: r.Id, GrantID: r.GrantId, Version: r.Version, TermsHash: r.TermsHash, PayloadVersion: r.PayloadVersion,
		State: string(r.State), CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, EndedAt: r.EndedAt, LastRefusedAt: r.LastRefusedAt, CeremonyURL: r.CeremonyUrl,
	}
	if rb := r.RequestedBy; rb != nil {
		d.RequestedBy = GrantSigningRequesterDTO{rb.UserId, rb.AppId, rb.WorkerId, rb.SessionId}
	}
	return d
}

// UnsafeForTerminal reports a character that could act on a terminal when
// printed: C0, DEL and C1 controls (Cc), format characters (Cf) and the line
// and paragraph separators (Zl, Zp).
func UnsafeForTerminal(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return true
		}
	}
	return false
}

// DashOrCell prints a nullable cell neutralised, or a dash for none.
func DashOrCell(p *string) string {
	if p == nil {
		return "—"
	}
	return output.SafeCell(*p)
}

// CeremonyURLRefusal is the text-mode refusal for a ceremony URL that could act
// on a terminal: the URL is never rewritten, so text refuses and --json carries it.
func CeremonyURLRefusal() error {
	return exitcode.Newf(exitcode.Error, "the ceremony URL contains control or format characters and is not printed as text; use --json to read it exactly")
}
