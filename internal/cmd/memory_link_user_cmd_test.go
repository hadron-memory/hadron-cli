package cmd

import (
	"encoding/json"
	"strings"
	"testing"

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
	f.IOStreams.In = strings.NewReader("super-secret-data-key\n") // piped key, trimmed
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
	if vars["dataKey"] != "super-secret-data-key" {
		t.Errorf("dataKey = %v, want the stdin key trimmed", vars["dataKey"])
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
	if got := exitcode.FromError(err); got != exitcode.Usage {
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
