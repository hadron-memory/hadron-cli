package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The anonymous memory as it stands before promotion, and as it comes back
// after: the URN is RE-MINTED, so the two differ by design.
const (
	anonMemoryJSON = `{"id":"mem1","urn":"hrn:mem:acme.com:anon-7f3","name":"Session","shortDescription":null,
		"description":null,"class":"app","visibility":null,"organizationId":"org1","isEncrypted":false,
		"tags":[],"source":null,"syncStatus":null,"vectorIndexEnabled":false,"maxRevCount":0,
		"data":null,"schema":null,"createdAt":"2026-07-27T00:00:00Z","updatedAt":"2026-07-27T00:00:00Z"}`
	linkedMemoryJSON = `{"id":"mem1","urn":"hrn:mem:acme.com:k3f9x2","name":"Session","shortDescription":null,
		"class":"app","visibility":null,"organizationId":"org1","isEncrypted":false,"maxRevCount":0,
		"updatedAt":"2026-07-27T01:00:00Z"}`
	linkedEncryptedMemoryJSON = `{"id":"mem1","urn":"hrn:mem:acme.com:k3f9x2","name":"Session","shortDescription":null,
		"class":"private","visibility":null,"organizationId":"org1","isEncrypted":true,"maxRevCount":0,
		"updatedAt":"2026-07-27T01:00:00Z"}`
)

func linkUserFakes(linked string) map[string]string {
	return map[string]string{
		"GetMemory":        `{"data":{"memory":` + anonMemoryJSON + `}}`,
		"LinkMemoryToUser": `{"data":{"linkMemoryToUser":` + linked + `}}`,
	}
}

func TestMemoryLinkUser(t *testing.T) {
	gql, captured := captureGraphQL(t, linkUserFakes(linkedMemoryJSON))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3",
		"--external-user", "auth0|abc123", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var vars map[string]any
	if err := json.Unmarshal(captured["LinkMemoryToUser"], &vars); err != nil {
		t.Fatalf("captured vars not JSON: %v", err)
	}
	// The ref is canonicalized, not pre-resolved to a PK — the mutation
	// resolves an id or a URN itself.
	if vars["memoryId"] != "hrn:mem:acme.com:anon-7f3" {
		t.Errorf("memoryId = %v, want the canonical URN", vars["memoryId"])
	}
	if vars["externalUserId"] != "auth0|abc123" {
		t.Errorf("externalUserId = %v", vars["externalUserId"])
	}
	// No --data-key: the optional variable must be OMITTED, not null, so the
	// server can't read it as an explicit value.
	if _, present := vars["dataKey"]; present {
		t.Errorf("dataKey must be omitted when --data-key is unset, got %v", vars["dataKey"])
	}

	var dto struct {
		Memory      struct{ ID, URN, Class string } `json:"memory"`
		PreviousURN string                          `json:"previousUrn"`
		Encrypted   bool                            `json:"encrypted"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.Memory.URN != "hrn:mem:acme.com:k3f9x2" {
		t.Errorf("memory.urn = %q, want the re-minted URN", dto.Memory.URN)
	}
	// previousUrn is the point of the DTO: the caller holds the old URN and
	// must learn it stopped resolving.
	if dto.PreviousURN != "hrn:mem:acme.com:anon-7f3" {
		t.Errorf("previousUrn = %q, want the pre-promotion URN", dto.PreviousURN)
	}
	if dto.Encrypted {
		t.Error("encrypted should be false without --data-key")
	}
}

func TestMemoryLinkUserWithDataKey(t *testing.T) {
	gql, captured := captureGraphQL(t, linkUserFakes(linkedEncryptedMemoryJSON))
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(linkUserDataKey + "\n") // piped key, trimmed
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3",
		"--external-user", "u1", "--data-key", "-", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	if err := json.Unmarshal(captured["LinkMemoryToUser"], &vars); err != nil {
		t.Fatalf("captured vars not JSON: %v", err)
	}
	// #846: the key never rides the variables — header only (the header
	// assertion lives in TestMemoryLinkUserDataKeyRidesTheHeaderNotTheBody).
	if _, present := vars["dataKey"]; present {
		t.Errorf("dataKey must not be sent as a GraphQL variable, got %v", vars["dataKey"])
	}
	var dto struct {
		Memory    struct{ Class string } `json:"memory"`
		Encrypted bool                   `json:"encrypted"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	// The encrypted promotion lands the memory private, per spec 041.
	if !dto.Encrypted || dto.Memory.Class != "private" {
		t.Errorf("encrypted promotion should report encrypted + class private, got %+v", dto)
	}
}

// The re-mint is the surprising part of this command, so the human output
// must state it — both URNs, and that the old one is dead.
func TestMemoryLinkUserHumanOutputReportsTheReMint(t *testing.T) {
	gql, _ := captureGraphQL(t, linkUserFakes(linkedMemoryJSON))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3",
		"--external-user", "u1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"hrn:mem:acme.com:anon-7f3", "hrn:mem:acme.com:k3f9x2", "no longer resolves"} {
		if !strings.Contains(got, want) {
			t.Errorf("human output should mention %q: %q", want, got)
		}
	}
}

// An App key that can run the mutation need not satisfy memory(ref:), so a
// failed pre-read must not block the promotion — it only costs previousUrn.
func TestMemoryLinkUserSurvivesUnreadableMemory(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetMemory":        `{"data":{"memory":null}}`,
		"LinkMemoryToUser": `{"data":{"linkMemoryToUser":` + linkedMemoryJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3",
		"--external-user", "u1", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Memory      struct{ URN string } `json:"memory"`
		PreviousURN string               `json:"previousUrn"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.Memory.URN != "hrn:mem:acme.com:k3f9x2" {
		t.Errorf("the promotion must still complete, got %+v", dto)
	}
	if dto.PreviousURN != "" {
		t.Errorf("previousUrn should be empty when the pre-read missed, got %q", dto.PreviousURN)
	}
}

func TestMemoryLinkUserRequiresExternalUser(t *testing.T) {
	for _, args := range [][]string{
		{"memory", "link-user", "acme.com::kb", "--yes", "--server", "http://127.0.0.1:1"},
		{"memory", "link-user", "acme.com::kb", "--external-user", "  ", "--yes", "--server", "http://127.0.0.1:1"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Errorf("expected a usage error for %v", args)
		}
	}
}

// The promotion is irreversible, so it gates like the other destructive
// memory commands — and must make no call when the gate refuses.
func TestMemoryLinkUserRequiresYesNonInteractive(t *testing.T) {
	gql, captured := captureGraphQL(t, linkUserFakes(linkedMemoryJSON))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3",
		"--external-user", "u1", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a refusal without --yes in non-interactive mode")
	}
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
	if _, called := captured["LinkMemoryToUser"]; called {
		t.Error("LinkMemoryToUser must not be called when the confirmation gate refuses")
	}
}

// A user PAT is refused server-side (the mutation gates on ctx.appId); that
// error must reach the caller rather than being swallowed.
func TestMemoryLinkUserAppKeyErrorPropagates(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetMemory":        `{"data":{"memory":` + anonMemoryJSON + `}}`,
		"LinkMemoryToUser": `{"errors":[{"message":"Forbidden: App Key required"}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3",
		"--external-user", "u1", "--yes", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected the server's App-key refusal to propagate")
	}
	if !strings.Contains(err.Error(), "App Key") {
		t.Errorf("error should name the App-key requirement, got %v", err)
	}
}

// `--data-key "$KEY"` with an unset KEY must fail, not silently skip
// encryption. Presence is what counts, not the value: treating an empty
// value as "flag absent" would complete an IRREVERSIBLE promotion leaving
// content plaintext while the caller believed it was encrypted (#301 review).
func TestMemoryLinkUserRejectsEmptyDataKey(t *testing.T) {
	gql, captured := captureGraphQL(t, linkUserFakes(linkedMemoryJSON))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3",
		"--external-user", "u1", "--data-key", "", "--yes", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a usage error for an explicitly empty --data-key")
	}
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
	// Critically, the irreversible promotion must not have run.
	if _, called := captured["LinkMemoryToUser"]; called {
		t.Error("an empty data key must be rejected BEFORE the promotion runs")
	}
}

// A whitespace-only key is equally empty once trimmed.
func TestMemoryLinkUserRejectsBlankStdinDataKey(t *testing.T) {
	gql, captured := captureGraphQL(t, linkUserFakes(linkedMemoryJSON))
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("   \n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3",
		"--external-user", "u1", "--data-key", "-", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a usage error for a blank piped data key")
	}
	if _, called := captured["LinkMemoryToUser"]; called {
		t.Error("a blank data key must be rejected BEFORE the promotion runs")
	}
}

// ---- #846: the data key moves to the x-hadron-operation-key header ----
//
// hadron-server#1817 (first #1796 slice) supplies the key per request through
// the header and deprecates the dataKey argument; the removal slice deletes
// the argument, which breaks EVERY document still naming it — even to omit
// it, even for a plaintext promotion. So the CLI's assertions here are about
// where key material may appear at all: this one request's header, and
// nowhere else.

// linkUserDataKey is a well-formed operation key: canonical base64 for exactly
// 32 bytes, the shape hadron-server's requestOperationKey parser demands
// (43 base64 characters plus one '=').
var linkUserDataKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))

// capturedLinkUserRequest records one GraphQL request the fake saw, in arrival
// order: the operation, the RAW body (document + variables — the non-leakage
// assertions must read the document, not just variables), and the
// x-hadron-operation-key header.
type capturedLinkUserRequest struct {
	Op    string
	Body  string
	OpKey string
}

// captureLinkUserGraphQL is captureGraphQL for the link-user wire contract:
// ordered, with headers and raw bodies, because #846 asserts WHERE the key
// rides (this request's header) and where it must never appear (any body, any
// other request — before or after).
func captureLinkUserGraphQL(t *testing.T, responses map[string]string) (*httptest.Server, *[]capturedLinkUserRequest) {
	t.Helper()
	var reqs []capturedLinkUserRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		var envelope struct {
			OperationName string `json:"operationName"`
		}
		_ = json.Unmarshal(body, &envelope)
		reqs = append(reqs, capturedLinkUserRequest{
			Op:    envelope.OperationName,
			Body:  string(body),
			OpKey: r.Header.Get("X-Hadron-Operation-Key"),
		})
		resp, ok := responses[envelope.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", envelope.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(server.Close)
	return server, &reqs
}

// The ONE place the key may appear is the promotion request's header: the
// document and variables must not name dataKey at all (naming it, even
// omitted, is what the removal slice breaks), the key value must be in no
// body, and the GetMemory pre-read must go out clean — the header is per-call,
// never a client default.
func TestMemoryLinkUserDataKeyRidesTheHeaderNotTheBody(t *testing.T) {
	gql, reqs := captureLinkUserGraphQL(t, linkUserFakes(linkedEncryptedMemoryJSON))
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader(linkUserDataKey + "\n") // piped, trimmed
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3",
		"--external-user", "u1", "--data-key", "-", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var promotion, preRead *capturedLinkUserRequest
	for i := range *reqs {
		switch (*reqs)[i].Op {
		case "LinkMemoryToUser":
			promotion = &(*reqs)[i]
		case "GetMemory":
			preRead = &(*reqs)[i]
		}
	}
	if promotion == nil {
		t.Fatal("LinkMemoryToUser was never called")
	}
	if promotion.OpKey != linkUserDataKey {
		t.Errorf("x-hadron-operation-key = %q on the promotion, want the trimmed key", promotion.OpKey)
	}
	if strings.Contains(promotion.Body, "dataKey") {
		t.Errorf("the promotion document must not name dataKey (the removal slice breaks every document that does):\n%s", promotion.Body)
	}
	if strings.Contains(promotion.Body, linkUserDataKey) {
		t.Errorf("the key leaked into the promotion request body:\n%s", promotion.Body)
	}
	if preRead != nil {
		// The pre-read runs BEFORE the promotion; a per-call header means it
		// must not carry the key in either spelling.
		if preRead.OpKey != "" {
			t.Errorf("the GetMemory pre-read must not carry the operation key, got %q", preRead.OpKey)
		}
		if strings.Contains(preRead.Body, linkUserDataKey) {
			t.Errorf("the key leaked into the pre-read body:\n%s", preRead.Body)
		}
	}
}

// A plaintext promotion names no key anywhere: no header on ANY request, and
// no dataKey in ANY document — the removal must not break plaintext callers
// "even plaintext ones" (issue #846, on why naming the argument is itself the
// breakage).
func TestMemoryLinkUserPlaintextNamesNoKeyAnywhere(t *testing.T) {
	gql, reqs := captureLinkUserGraphQL(t, linkUserFakes(linkedMemoryJSON))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3",
		"--external-user", "u1", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, r := range *reqs {
		if r.OpKey != "" {
			t.Errorf("%s carried x-hadron-operation-key %q", r.Op, r.OpKey)
		}
		if strings.Contains(r.Body, "dataKey") {
			t.Errorf("%s still names dataKey:\n%s", r.Op, r.Body)
		}
	}
}

// A malformed key is refused LOCALLY, before any request — including the
// GetMemory pre-read. The server's header parser demands canonical base64 for
// exactly 32 bytes and answers anything else BAD_UNLOCK_INPUT; saying that
// client-side is the same refusal, one round-trip earlier, and without
// echoing the value.
func TestMemoryLinkUserRejectsMalformedDataKey(t *testing.T) {
	// Decodes to the same 32 bytes as the all-'A' key but is not the canonical
	// spelling: the final character's low bits are discarded padding.
	nonCanonical := strings.Repeat("A", 42) + "B="
	// URL-safe base64 uses '-'/'_' where the header demands '+'/'/'.
	urlSafe := base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{0xfb}, 32))
	for _, tc := range []struct{ name, key string }{
		{"not base64", "not a key!"},
		{"too short (decodes to 8 bytes)", base64.StdEncoding.EncodeToString([]byte("12345678"))},
		{"missing padding", strings.Repeat("A", 43)},
		{"non-canonical spelling", nonCanonical},
		{"url-safe alphabet", urlSafe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, reqs := captureLinkUserGraphQL(t, linkUserFakes(linkedEncryptedMemoryJSON))
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3",
				"--external-user", "u1", "--data-key", tc.key, "--yes", "--json", "--server", gql.URL})
			err := root.Execute()
			if err == nil {
				t.Fatalf("a malformed key (%s) must be refused", tc.name)
			}
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Errorf("exit code = %d, want %d (Usage)", got, exitcode.Usage)
			}
			if strings.Contains(err.Error(), tc.key) {
				t.Errorf("the refusal must not echo the key value: %v", err)
			}
			if len(*reqs) != 0 {
				t.Errorf("a malformed key must be refused BEFORE any request; the server saw %d", len(*reqs))
			}
		})
	}
}

// net/http strips Authorization and cookies on a cross-host redirect but
// FORWARDS custom headers — the real hazard in moving the key into
// x-hadron-operation-key (Ralion's §10 pre-read, chat #9428). A key-bearing
// promotion that gets 307'd to another host must be refused before the
// replay: the second host must never see the header. The key-free pre-read
// may still follow the redirect.
//
// The second host answers the pre-read with a REAL memory fixture and the
// refusal is asserted as api.ErrRedirectPolicy (#847 cycle 0, Xavier P3-4):
// a future refactor that aborts on a null pre-read would otherwise pass this
// test vacuously — no promotion request, no cross-host key, green.
func TestMemoryLinkUserKeyDoesNotFollowACrossHostRedirect(t *testing.T) {
	var otherHeaders, otherBodies []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHeaders = append(otherHeaders, r.Header.Get("X-Hadron-Operation-Key"))
		body, _ := io.ReadAll(r.Body)
		otherBodies = append(otherBodies, string(body))
		resp := `{"data":{"memory":` + anonMemoryJSON + `}}`
		if strings.Contains(string(body), "linkMemoryToUser") {
			resp = `{"data":{"linkMemoryToUser":` + linkedEncryptedMemoryJSON + `}}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(other.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/graphql", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(origin.Close)

	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader(linkUserDataKey + "\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3",
		"--external-user", "u1", "--data-key", "-", "--yes", "--json", "--server", origin.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("the key-bearing promotion must be refused rather than replayed to another host")
	}
	if !errors.Is(err, api.ErrRedirectPolicy) {
		t.Errorf("the refusal must be the redirect policy (got %v); a network failure would pass this test for the wrong reason", err)
	}
	for i, h := range otherHeaders {
		if h != "" {
			t.Errorf("request %d reached the second host carrying x-hadron-operation-key", i)
		}
	}
	for i, b := range otherBodies {
		if strings.Contains(b, linkUserDataKey) {
			t.Errorf("request %d reached the second host with the key in its body", i)
		}
	}
}

// #847 cycle 0 (Xavier P1 = Ralion's §10 P2): a header fails silently where
// an argument failed loudly. A pre-#1817 server or a header-stripping proxy
// ignores x-hadron-operation-key, the document legitimately omits dataKey,
// and the IRREVERSIBLE promotion completes unencrypted while the caller
// asked for encryption — exiting 0 with success output. A supplied key plus
// isEncrypted=false in the response must be a loud non-zero failure that
// states plainly what did and did not happen. It cannot false-alarm on a
// #1817+ server: there, a key-supplied promotion either lands encrypted or
// the request itself errors (BAD_UNLOCK_INPUT / WRONG_KEY).
func TestMemoryLinkUserUnappliedKeyFailsLoudly(t *testing.T) {
	gql, _ := captureLinkUserGraphQL(t, linkUserFakes(linkedMemoryJSON)) // isEncrypted:false
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(linkUserDataKey + "\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "link-user", "acme.com::anon-7f3",
		"--external-user", "u1", "--data-key", "-", "--yes", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a promotion that ignored the supplied key must fail loudly, not exit 0")
	}
	if got := exitCodeFor(err); got == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if strings.Contains(out.String(), "✓ linked") {
		t.Errorf("no success output may be written before the failure, got:\n%s", out.String())
	}
	msg := err.Error()
	// The message must be honest about both halves: the promotion DID happen
	// (irreversibly), the encryption did not.
	if !strings.Contains(msg, "unencrypted") || !strings.Contains(msg, "irreversible") {
		t.Errorf("the message must state the promotion completed unencrypted and is irreversible, got %q", msg)
	}
	if strings.Contains(msg, linkUserDataKey) {
		t.Errorf("the failure must not echo the key: %v", err)
	}
}
