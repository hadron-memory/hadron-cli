package node

import (
	"context"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// The signature-status summary folded into `node get` (cli#878 part 3).
//
// The gate is the whole point: the fold runs only when the server ADVERTISES
// signing through GET /capabilities (`node-signing`, hadron-server#1925,
// mounted by #1930), so a switched-off or older server costs no extra
// refused round trip — the signature query would be an unknown-field error
// there. An unreadable status is null (text: unavailable), never a guessed
// zero — the commentSummary precedent.
//
// v1 folds the single-ref object form only: a batch must not pay one status
// query per node.

// SigningCapabilityProbe answers whether the server advertises the
// `node-signing` switch. Its failure reads as "not advertised".
//
// Package-level so command tests can inject a fake (house convention).
var SigningCapabilityProbe = func(ctx context.Context, baseURL string) (bool, error) {
	caps, err := api.Capabilities(ctx, baseURL)
	if err != nil {
		return false, err
	}
	return caps["node-signing"], nil
}

// signatureSummaryDTO is the stable --json shape of the fold: counts and the
// node-level state, never key material and never a signer's details (the
// full status stays `node signature status`).
type signatureSummaryDTO struct {
	State             string `json:"state"`
	IntegrityProblems int    `json:"integrityProblems"`
	Signatures        int    `json:"signatures"`
	Verified          int    `json:"verified"`
}

// loadSignatureSummary folds the summary into the single-ref node get, gated
// on the capability probe. Any failure past the gate leaves the summary nil
// (rendered "unavailable") — the node read itself already succeeded.
func loadSignatureSummary(ctx context.Context, f *cmdutil.Factory, client graphql.Client, ref string, dto *nodeDetailDTO) {
	server, err := f.Server()
	if err != nil {
		return
	}
	advertised, err := SigningCapabilityProbe(ctx, server)
	if err != nil || !advertised {
		return
	}
	resp, err := gen.NodeSignatureStatus(ctx, client, ref)
	if err != nil || resp == nil || resp.Node == nil || resp.Node.SignatureStatus == nil {
		// Advertised but unreadable: the human line says so; --json stays
		// null (never a guessed zero). Not advertised at all prints nothing.
		dto.signatureReadFailed = true
		return
	}
	s := resp.Node.SignatureStatus
	summary := signatureSummaryDTO{
		IntegrityProblems: s.IntegrityProblems,
		Signatures:        len(s.Signatures),
	}
	if s.State != nil {
		summary.State = string(*s.State)
	}
	for _, sig := range s.Signatures {
		if sig.State == gen.SignatureStateCurrent {
			summary.Verified++
		}
	}
	dto.SignatureSummary = &summary
}

// renderSignatureLine prints the fold's human line, after the feedback line
// — the same "advisory snapshot" register.
func renderSignatureLine(w io.Writer, dto nodeDetailDTO) {
	if dto.SignatureSummary == nil {
		if dto.signatureReadFailed {
			fmt.Fprintln(w, "  signature: unavailable")
		}
		return
	}
	s := dto.SignatureSummary
	if s.Signatures == 0 {
		fmt.Fprintf(w, "  signature: %s (no signatures)\n", stateOrNone(s.State))
		return
	}
	fmt.Fprintf(w, "  signature: %s (%d signatures; %d verified)\n", stateOrNone(s.State), s.Signatures, s.Verified)
}

func stateOrNone(state string) string {
	if state == "" {
		return "none"
	}
	return state
}
