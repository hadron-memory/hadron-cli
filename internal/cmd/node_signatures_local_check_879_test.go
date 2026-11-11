package cmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/approvalhash"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#879 — `node signatures --check` rebuilds each record's v3 payload and
// challenge locally (cor:dmo:130:01). v3 only (R80), rebuild only: the server's
// state stays authoritative and the local result can only add a failure.

const (
	l879NodeID  = "01a10408afb6716c80d6944e8845467b"
	l879Request = "AAAAAAAAAAAAAAAAAAAAAA"
	l879Title   = "Flaky CI"
	l879Content = "line one\r\nline two\n"

	// The fixed sentences (Xylon, #23915), byte for byte.
	l879HelpSentence  = "Rebuild each signature's v3 payload and challenge locally and check that its inputs agree. This does not verify the passkey assertion: the server re-verifies signatures on read, and its state, shown unchanged, is authoritative."
	l879HumanSentence = "payload rebuilt locally; the passkey assertion is verified by the server, not by this CLI"
)

func l879GoodHash() string {
	title, content := l879Title, l879Content
	return approvalhash.Hash(approvalhash.Fields{Title: &title, Content: &content})
}

func l879Sig(version string, revision int) string {
	return fmt.Sprintf(`{"id":"sig1","state":"CURRENT","revision":%d,"verifiedAt":"2026-10-14T06:05:00Z","passkeyLabel":"YubiKey 5C",`+
		`"passkeyRevokedAt":null,"payloadVersion":%q,"signingRequestId":%q,"signer":{"handle":"ada","urn":"hrn:user:ada"}}`, revision, version, l879Request)
}

func l879Request1(pinned string) string {
	return fmt.Sprintf(`{"data":{"signingRequest":{"id":%q,"nodeId":%q,"nodeUrn":"hrn:node:acme.com:kb:findings:flaky-ci","revision":4,`+
		`"approvalHash":%q,"payloadVersion":"hadron-signature-v3","state":"SIGNED","createdAt":"2026-10-14T06:00:00Z",`+
		`"expiresAt":"2026-10-15T06:00:00Z","endedAt":null,"namedSigner":null,"requestedBy":null,"signature":null}}}`, l879Request, l879NodeID, pinned)
}

const l879NullRequest = `{"data":{"signingRequest":null}}`

func l879RawNode() string {
	return `{"data":{"node":{"id":"n1","urn":"hrn:node:acme.com:kb:findings:flaky-ci","memoryId":"mem1","loc":"findings:flaky-ci",` +
		`"name":"Flaky CI","abstract":null,"content":"line one\r\nline two\n","nodeType":"info"}}}`
}

// l879Run runs `node signatures --check --json` (or without --check) and
// returns the exit code, stdout and the operations sent.
func l879Run(t *testing.T, responses map[string]string, args ...string) (int, string, map[string]json.RawMessage) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"node", "signatures", nodeURN, "--server", gql.URL}, args...))
	err := root.Execute()
	return exitCodeFor(err), out.String(), captured
}

func l879Responses(state string, integrity int, sig, request string) map[string]string {
	return map[string]string{
		"ResolveUrn":          resolveNodeJSON,
		"NodeSignatureStatus": signatureStatusJSON(state, integrity, sig),
		"GetSigningRequest":   request,
		"GetNodeRaw":          l879RawNode(),
	}
}

func l879LocalChecks(t *testing.T, out string) []string {
	t.Helper()
	var dto struct {
		Signatures []struct {
			LocalCheck *string `json:"localCheck"`
		} `json:"signatures"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var got []string
	for _, s := range dto.Signatures {
		if s.LocalCheck == nil {
			got = append(got, "<absent>")
		} else {
			got = append(got, *s.LocalCheck)
		}
	}
	return got
}

type l879Local struct {
	name, want string
	sig        string
	request    string
}

func l879Locals() []l879Local {
	return []l879Local{
		{"consistent", "PAYLOAD_CONSISTENT", l879Sig("hadron-signature-v3", 4), l879Request1(l879GoodHash())},
		{"hash mismatch", "HASH_MISMATCH", l879Sig("hadron-signature-v3", 4), l879Request1(strings.Repeat("a", 64))},
		{"v1 record", "UNSUPPORTED_VERSION", l879Sig("hadron-signature-v1", 4), l879Request1(l879GoodHash())},
		{"v2 record", "UNSUPPORTED_VERSION", l879Sig("hadron-signature-v2", 4), l879Request1(l879GoodHash())},
		{"request unreadable", "UNAVAILABLE", l879Sig("hadron-signature-v3", 4), l879NullRequest},
	}
}

func TestLocalCheckStatesPerRecord(t *testing.T) {
	for _, tc := range l879Locals() {
		t.Run(tc.name, func(t *testing.T) {
			_, out, _ := l879Run(t, l879Responses("CURRENT", 0, tc.sig, tc.request), "--check", "--json")
			if got := l879LocalChecks(t, out); len(got) != 1 || got[0] != tc.want {
				t.Errorf("localCheck = %v, want [%s]", got, tc.want)
			}
		})
	}
}

// A record whose signed revision text is unreadable is reported, never dropped.
func TestLocalCheckRevisionUnreadableIsUnavailable(t *testing.T) {
	r := l879Responses("CURRENT", 0, l879Sig("hadron-signature-v3", 4), l879Request1(l879GoodHash()))
	r["GetNodeRaw"] = `{"data":{"node":null}}`
	_, out, _ := l879Run(t, r, "--check", "--json")
	if got := l879LocalChecks(t, out); len(got) != 1 || got[0] != "UNAVAILABLE" {
		t.Errorf("localCheck = %v", got)
	}
	// An earlier revision absent from the page is the same.
	r = l879Responses("EARLIER", 0, l879Sig("hadron-signature-v3", 3), l879Request1(l879GoodHash()))
	r["NodeRevisionTexts"] = `{"data":{"nodeRevisionsPage":{"hasMore":false,"items":[{"revNo":2,"name":"x","abstract":null,"content":"y"}]}}}`
	_, out, _ = l879Run(t, r, "--check", "--json")
	if got := l879LocalChecks(t, out); len(got) != 1 || got[0] != "UNAVAILABLE" {
		t.Errorf("localCheck = %v", got)
	}
}

// A signature on an earlier revision is checked against that revision's own
// stored text, not the node's current text.
func TestLocalCheckUsesTheSignedRevisionsOwnText(t *testing.T) {
	r := l879Responses("EARLIER", 0, l879Sig("hadron-signature-v3", 3), l879Request1(l879GoodHash()))
	r["NodeRevisionTexts"] = `{"data":{"nodeRevisionsPage":{"hasMore":false,"items":[` +
		`{"revNo":4,"name":"Edited","abstract":null,"content":"newer"},` +
		`{"revNo":3,"name":"Flaky CI","abstract":null,"content":"line one\r\nline two\n"}]}}}`
	r["GetNodeRaw"] = `{"data":{"node":{"id":"n1","urn":"u","memoryId":"mem1","loc":"l","name":"Edited","abstract":null,"content":"newer","nodeType":"info"}}}`
	_, out, captured := l879Run(t, r, "--check", "--json")
	if got := l879LocalChecks(t, out); len(got) != 1 || got[0] != "PAYLOAD_CONSISTENT" {
		t.Errorf("localCheck = %v, want PAYLOAD_CONSISTENT", got)
	}
	if _, sent := captured["NodeRevisionTexts"]; !sent {
		t.Error("the earlier revision's text was not read from its snapshot")
	}
}

// The combined exit contract (issue #879 table): the server's refusal is kept
// and a local result can only add a failure.
func TestCheckExitTable(t *testing.T) {
	type roll struct {
		name      string
		state     string
		integrity int
		fails     bool
	}
	rolls := []roll{
		{"current", "CURRENT", 0, false},
		{"no state", "null", 0, true},
		{"earlier", "EARLIER", 0, true},
		{"pending", "PENDING", 0, true},
		{"cancelled", "CANCELLED", 0, true},
		{"not applicable", "NOT_APPLICABLE", 0, true},
		{"current with integrity problems", "CURRENT", 2, true},
	}
	for _, r := range rolls {
		for _, l := range l879Locals() {
			want := exitcode.OK
			if r.fails || l.want == "HASH_MISMATCH" || l.want == "UNSUPPORTED_VERSION" {
				want = exitcode.Conflict
			}
			t.Run(r.name+"/"+l.name, func(t *testing.T) {
				sigs := l.sig
				if r.state == "null" {
					sigs = ""
				}
				code, out, _ := l879Run(t, l879Responses(r.state, r.integrity, sigs, l.request), "--check", "--json")
				if code != want {
					t.Errorf("exit = %d, want %d", code, want)
				}
				// Every record is printed before the exit.
				if !strings.Contains(out, `"integrityProblems"`) {
					t.Errorf("status not printed: %s", out)
				}
			})
		}
	}
	// No records listed under a CURRENT roll-up passes.
	if code, _, _ := l879Run(t, l879Responses("CURRENT", 0, "", l879NullRequest), "--check", "--json"); code != exitcode.OK {
		t.Errorf("CURRENT with no records: exit = %d, want 0", code)
	}
}

// The server's state is shown unchanged: removing the local keys from a
// --check result leaves exactly the output without --check, for every
// local result and every server state.
func TestLocalCheckNeverTouchesTheServersState(t *testing.T) {
	for _, state := range []string{"CURRENT", "EARLIER", "PENDING", "CANCELLED", "NOT_APPLICABLE"} {
		for _, l := range l879Locals() {
			t.Run(state+"/"+l.name, func(t *testing.T) {
				sig := strings.Replace(l.sig, `"CURRENT"`, `"`+state+`"`, 1)
				resp := l879Responses(state, 1, sig, l.request)
				_, plain, _ := l879Run(t, resp, "--json")
				_, checked, _ := l879Run(t, resp, "--check", "--json")
				var a, b map[string]any
				if err := json.Unmarshal([]byte(plain), &a); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(checked), &b); err != nil {
					t.Fatal(err)
				}
				for _, s := range b["signatures"].([]any) {
					delete(s.(map[string]any), "localCheck")
				}
				ja, _ := json.Marshal(a)
				jb, _ := json.Marshal(b)
				if string(ja) != string(jb) {
					t.Errorf("--check changed the server's fields:\n plain   %s\n checked %s", ja, jb)
				}
			})
		}
	}
}

// Without --check nothing changes: no local key, no extra reads, and today's
// human and JSON text byte for byte.
func TestWithoutCheckOutputIsUnchanged(t *testing.T) {
	resp := l879Responses("CURRENT", 0, l879Sig("hadron-signature-v3", 4), l879Request1(l879GoodHash()))
	code, out, captured := l879Run(t, resp, "--json")
	if code != exitcode.OK {
		t.Fatalf("exit = %d", code)
	}
	for _, op := range []string{"GetSigningRequest", "GetNodeRaw", "NodeRevisionTexts"} {
		if _, sent := captured[op]; sent {
			t.Errorf("%s sent without --check", op)
		}
	}
	if strings.Contains(out, "localCheck") {
		t.Errorf("localCheck present without --check: %s", out)
	}
	wantJSON := `{
  "nodeId": "n1",
  "urn": "hrn:node:acme.com:kb:findings:flaky-ci",
  "loc": "findings:flaky-ci",
  "revision": 4,
  "state": "CURRENT",
  "integrityProblems": 0,
  "signatures": [
    {
      "id": "sig1",
      "state": "CURRENT",
      "revision": 4,
      "verifiedAt": "2026-10-14T06:05:00Z",
      "signer": {
        "handle": "ada",
        "urn": "hrn:user:ada"
      },
      "passkeyLabel": "YubiKey 5C",
      "passkeyRevokedAt": null,
      "payloadVersion": "hadron-signature-v3",
      "signingRequestId": "AAAAAAAAAAAAAAAAAAAAAA"
    }
  ]
}
`
	if out != wantJSON {
		t.Errorf("--json without --check changed:\n%s", out)
	}
	_, human, _ := l879Run(t, resp)
	if strings.Contains(human, "local check") || strings.Contains(human, l879HumanSentence) {
		t.Errorf("human output grew without --check: %s", human)
	}
}

var l879Forbidden = regexp.MustCompile(`(?i)verified|valid|authentic`)

// The wording is bound to the rebuild-only boundary: the two fixed sentences
// verbatim, and no other claim of verification anywhere in --check output.
func TestCheckWordingClaimsNoLocalVerification(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	sub, _, err := root.Find([]string{"node", "signatures"})
	if err != nil {
		t.Fatal(err)
	}
	if usage := sub.Flags().Lookup("check").Usage; !strings.Contains(usage, l879HelpSentence) {
		t.Errorf("--check help = %q", usage)
	}
	if !strings.Contains(sub.Long, "exits 5 unless the roll-up state is\nCURRENT and there are no integrity problems") {
		t.Errorf("the exit sentence was dropped from the help: %s", sub.Long)
	}

	for _, l := range l879Locals() {
		t.Run(l.name, func(t *testing.T) {
			resp := l879Responses("CURRENT", 0, l.sig, l.request)
			_, human, _ := l879Run(t, resp, "--check")
			_, js, _ := l879Run(t, resp, "--check", "--json")
			if l.want == "PAYLOAD_CONSISTENT" && !strings.Contains(human, l879HumanSentence) {
				t.Errorf("human output lacks the fixed line: %s", human)
			}
			for _, out := range []string{human, js} {
				// Remove the echoed server states and the one fixed sentence.
				scrubbed := strings.ReplaceAll(out, l879HumanSentence, "")
				scrubbed = regexp.MustCompile(`(?m)^\s*("state": "[A-Z_]+",?|signatures: [A-Z_a-z]+|.*\bCURRENT\b.*)$`).ReplaceAllString(scrubbed, "")
				scrubbed = strings.ReplaceAll(scrubbed, "re-verified", "")
				if m := l879Forbidden.FindString(scrubbed); m != "" {
					t.Errorf("output claims %q: %s", m, scrubbed)
				}
			}
		})
	}
}

// Rebuild only: the command reads no assertion or key material.
func TestCheckRequestsNoAssertionOrKeyMaterial(t *testing.T) {
	resp := l879Responses("CURRENT", 0, l879Sig("hadron-signature-v3", 4), l879Request1(l879GoodHash()))
	_, out, captured := l879Run(t, resp, "--check", "--json")
	allowed := map[string]bool{"ResolveUrn": true, "NodeSignatureStatus": true, "GetSigningRequest": true, "GetNodeRaw": true, "NodeRevisionTexts": true}
	for op, vars := range captured {
		if !allowed[op] {
			t.Errorf("unexpected operation %s %s", op, vars)
		}
	}
	for _, word := range []string{"authenticatorData", "clientDataJSON", "publicKey", "cose", "assertion"} {
		if strings.Contains(strings.ToLower(out), strings.ToLower(word)) {
			t.Errorf("output mentions %q: %s", word, out)
		}
	}
}

// An unsupported record is decided from its version alone: no request or text
// is read for it, and nothing falls back to a v1 rebuild.
func TestUnsupportedVersionReadsNothingElse(t *testing.T) {
	resp := l879Responses("CURRENT", 0, l879Sig("hadron-signature-v1", 4), l879Request1(l879GoodHash()))
	_, _, captured := l879Run(t, resp, "--check", "--json")
	for _, op := range []string{"GetSigningRequest", "GetNodeRaw", "NodeRevisionTexts"} {
		if _, sent := captured[op]; sent {
			t.Errorf("%s sent for a v1 record", op)
		}
	}
}
