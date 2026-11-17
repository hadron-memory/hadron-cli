package node

import (
	"context"
	"errors"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/approvalhash"
	"github.com/hadron-memory/hadron-cli/internal/sigpayload"
)

// cli#879 — the local half of `node signatures --check` (cor:dmo:130:01).
// It rebuilds each record's v3 payload and challenge and checks that the
// inputs agree. It is rebuild-only: no assertion or key material is read, and
// the server's re-verified-on-read state stays authoritative.

// The localCheck states. None of them claims verification.
const (
	localPayloadConsistent  = "PAYLOAD_CONSISTENT"
	localHashMismatch       = "HASH_MISMATCH"
	localUnsupportedVersion = "UNSUPPORTED_VERSION"
	localUnavailable        = "UNAVAILABLE"
)

// localFails reports whether a local result adds a --check failure. Neither
// UNAVAILABLE nor PAYLOAD_CONSISTENT does, and neither can lift a server
// failure.
func localFails(state string) bool {
	return state == localHashMismatch || state == localUnsupportedVersion
}

const (
	// The fixed sentences (Xylon, cli#879 #23915).
	checkFlagUsage = "Rebuild each signature's v3 payload and challenge locally and check that its inputs agree. " +
		"This does not verify the passkey assertion: the server re-verifies signatures on read, and its state, shown unchanged, is authoritative."
	localCheckHumanLine = "payload rebuilt locally; the passkey assertion is verified by the server, not by this CLI"

	// revisionPageSize bounds each read of a node's history while looking for
	// one signed revision.
	revisionPageSize = 100

	// maxRevisionPages bounds the whole scan (cli#1907): a server that keeps
	// answering hasMore must not hold the CLI forever. 100 pages of 100 cover
	// 10,000 revisions, far past any real node.
	maxRevisionPages = 100
)

// errSnapshotHistoryCapped is returned when the history is longer than the
// page cap. The snapshot could not be rebuilt, so the check has no outcome to
// report for the signature: it is an error, never a signature state. In
// particular it is not HASH_MISMATCH (invalid is reserved for an assertion
// failing verification or an inconsistent record, cor:dmo:130:04), and it is
// not UNAVAILABLE or PAYLOAD_CONSISTENT, since the verifier rebuilds the bytes
// byte for byte or does not verify (cor:dmo:130:01).
var errSnapshotHistoryCapped = errors.New("could not rebuild the signed snapshot: the node's revision history is longer than this check will read")

// signedText is one revision's stored title, abstract and content.
type signedText struct {
	title, abstract, content *string
}

type localChecker struct {
	ctx      context.Context
	client   graphql.Client
	nodeRef  string
	nodeID   string
	requests map[string]*gen.GetSigningRequestSigningRequest
	texts    map[int]*signedText
	history  map[int]*signedText
	scanned  bool
	scanErr  error // set when the history scan was capped

	currentRead bool
	currentRev  int
	current     *signedText
}

func newLocalChecker(ctx context.Context, client graphql.Client, nodeRef, nodeID string) *localChecker {
	return &localChecker{ctx: ctx, client: client, nodeRef: nodeRef, nodeID: nodeID,
		requests: map[string]*gen.GetSigningRequestSigningRequest{}, texts: map[int]*signedText{}}
}

// check classifies one signature record. A transport or server failure other
// than "unreadable" is returned, never folded into UNAVAILABLE.
func (c *localChecker) check(sig signatureDTO) (string, error) {
	if sigpayload.CheckVersion(sig.PayloadVersion) != nil {
		return localUnsupportedVersion, nil
	}
	req, err := c.request(sig.SigningRequestID)
	if err != nil {
		return "", err
	}
	if req == nil {
		return localUnavailable, nil
	}
	// The request must be the one this signature names, for this node, at
	// the signed revision and at v3: a request that merely agrees on the
	// hash is not this record's request.
	if req.Id != sig.SigningRequestID || req.NodeId != c.nodeID || req.Revision != sig.Revision ||
		sigpayload.CheckVersion(req.PayloadVersion) != nil {
		return localHashMismatch, nil
	}
	text, err := c.text(sig.Revision)
	if err != nil {
		return "", err
	}
	if text == nil {
		return localUnavailable, nil
	}
	if approvalhash.Hash(approvalhash.Fields{Title: text.title, Abstract: text.abstract, Content: text.content}) != req.ApprovalHash {
		return localHashMismatch, nil
	}
	payload, err := sigpayload.BuildV3(req.NodeId, req.ApprovalHash, req.Id)
	if err != nil {
		// The inputs agree on the hash but cannot form a v3 payload: the
		// record is not locally consistent.
		return localHashMismatch, nil
	}
	_ = sigpayload.Challenge(payload)
	return localPayloadConsistent, nil
}

// unreadable reports a refusal that means "you cannot read this": reported as
// UNAVAILABLE, never fatal. Every error in the list must be one of them: a
// transport or internal failure, alone or beside a refusal, is not.
//
// Why exactly these three: they are the refusals a read of a signed node's
// text or its signing request answers when the caller cannot see it (the node
// is missing or hidden, the request is missing or hidden, or the caller has no
// read right). All three say "not readable by you", so the local check has
// nothing to rebuild from and reports UNAVAILABLE. Any other code, and any
// 5xx envelope even when it carries one of these (api.OnlyErrorCodes excludes
// it), may be a failure of the read rather than a refusal, so it stays fatal.
// The list is signing-specific; do not reuse it as a generic "unreadable".
func unreadable(err error) bool {
	return api.OnlyErrorCodes(err, "NODE_NOT_FOUND", "SIGNING_REQUEST_NOT_FOUND", "FORBIDDEN")
}

func (c *localChecker) request(id string) (*gen.GetSigningRequestSigningRequest, error) {
	if r, ok := c.requests[id]; ok {
		return r, nil
	}
	resp, err := gen.GetSigningRequest(c.ctx, c.client, id)
	if err != nil {
		if unreadable(err) {
			c.requests[id] = nil
			return nil, nil
		}
		return nil, mapSigningError(err)
	}
	c.requests[id] = resp.SigningRequest
	return resp.SigningRequest, nil
}

// text returns the stored text of the revision a signature names. The node's
// raw text is read together with its revision: while that is the signed
// revision it is used, and otherwise (the node has moved on, even between our
// own reads) the signed revision's snapshot is.
func (c *localChecker) text(revision int) (*signedText, error) {
	if t, ok := c.texts[revision]; ok {
		return t, nil
	}
	if !c.currentRead {
		c.currentRead = true
		resp, err := gen.NodeRawText(c.ctx, c.client, c.nodeRef)
		switch {
		case err != nil && unreadable(err):
		case err != nil:
			return nil, api.MapError(err)
		case resp.Node != nil:
			c.currentRev = resp.Node.Revision
			name := resp.Node.Name
			c.current = &signedText{title: &name, abstract: resp.Node.Abstract, content: resp.Node.Content}
		}
	}
	if c.current != nil && c.currentRev == revision {
		return c.current, nil
	}
	t, err := c.snapshotText(revision)
	if err != nil {
		return nil, err
	}
	c.texts[revision] = t
	return t, nil
}

func (c *localChecker) snapshotText(revision int) (*signedText, error) {
	if !c.scanned {
		c.scanned = true
		c.history = map[int]*signedText{}
		for offset, pages := 0, 0; ; {
			pages++
			limit, off := revisionPageSize, offset
			resp, err := gen.NodeRevisionTexts(c.ctx, c.client, c.nodeRef, &limit, &off)
			if err != nil {
				if unreadable(err) {
					break
				}
				return nil, api.MapError(err)
			}
			for _, it := range resp.NodeRevisionsPage.Items {
				if it != nil && it.RevNo != nil {
					name := it.Name
					c.history[*it.RevNo] = &signedText{title: &name, abstract: it.Abstract, content: it.Content}
				}
			}
			// Advance by what came back: a server may clamp the page size.
			offset += len(resp.NodeRevisionsPage.Items)
			if !resp.NodeRevisionsPage.HasMore || len(resp.NodeRevisionsPage.Items) == 0 {
				break
			}
			if pages >= maxRevisionPages {
				// More history than the cap: whatever was read is partial, so
				// no revision may be answered from it, now or for a later
				// signature.
				c.history, c.scanErr = nil, errSnapshotHistoryCapped
				break
			}
		}
	}
	if c.scanErr != nil {
		return nil, c.scanErr
	}
	return c.history[revision], nil
}
