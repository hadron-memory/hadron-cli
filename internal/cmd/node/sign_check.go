package node

import (
	"context"

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
	// one signed revision; the loop pages to exhaustion.
	revisionPageSize = 100
)

// signedText is one revision's stored title, abstract and content.
type signedText struct {
	title, abstract, content *string
}

type localChecker struct {
	ctx      context.Context
	client   graphql.Client
	nodeRef  string
	current  int
	requests map[string]*gen.GetSigningRequestSigningRequest
	texts    map[int]*signedText
	history  map[int]*signedText
	scanned  bool
}

func newLocalChecker(ctx context.Context, client graphql.Client, nodeRef string, currentRevision int) *localChecker {
	return &localChecker{ctx: ctx, client: client, nodeRef: nodeRef, current: currentRevision,
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

func (c *localChecker) request(id string) (*gen.GetSigningRequestSigningRequest, error) {
	if r, ok := c.requests[id]; ok {
		return r, nil
	}
	resp, err := gen.GetSigningRequest(c.ctx, c.client, id)
	if err != nil {
		if api.HasErrorCode(err, "SIGNING_REQUEST_NOT_FOUND") {
			c.requests[id] = nil
			return nil, nil
		}
		return nil, mapSigningError(err)
	}
	c.requests[id] = resp.SigningRequest
	return resp.SigningRequest, nil
}

// text returns the stored text of the revision a signature names: the node's
// own raw text while that revision is still current, otherwise its snapshot.
func (c *localChecker) text(revision int) (*signedText, error) {
	if t, ok := c.texts[revision]; ok {
		return t, nil
	}
	var t *signedText
	var err error
	if revision == c.current {
		t, err = c.currentText()
	} else {
		t, err = c.snapshotText(revision)
	}
	if err != nil {
		return nil, err
	}
	c.texts[revision] = t
	return t, nil
}

func (c *localChecker) currentText() (*signedText, error) {
	resp, err := gen.GetNodeRaw(c.ctx, c.client, c.nodeRef)
	if err != nil {
		return nil, api.MapError(err)
	}
	n := resp.Node
	if n == nil {
		return nil, nil
	}
	name := n.Name
	return &signedText{title: &name, abstract: n.Abstract, content: n.Content}, nil
}

func (c *localChecker) snapshotText(revision int) (*signedText, error) {
	if !c.scanned {
		c.scanned = true
		c.history = map[int]*signedText{}
		for offset := 0; ; offset += revisionPageSize {
			limit, off := revisionPageSize, offset
			resp, err := gen.NodeRevisionTexts(c.ctx, c.client, c.nodeRef, &limit, &off)
			if err != nil {
				return nil, api.MapError(err)
			}
			for _, it := range resp.NodeRevisionsPage.Items {
				if it != nil && it.RevNo != nil {
					name := it.Name
					c.history[*it.RevNo] = &signedText{title: &name, abstract: it.Abstract, content: it.Content}
				}
			}
			if !resp.NodeRevisionsPage.HasMore || len(resp.NodeRevisionsPage.Items) == 0 {
				break
			}
		}
	}
	return c.history[revision], nil
}
