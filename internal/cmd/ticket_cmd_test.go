package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const ticketJSON = `{"id":"tkt1","organizationId":"org1","appId":null,"action":"comm.outbound",
	"mintedBy":"u1","note":"nightly send budget","consumedByRunId":null,"consumedAt":null,
	"expiresAt":null,"createdAt":"2026-07-05T00:00:00Z"}`

func TestTicketMint(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MintActionTickets": `{"data":{"mintActionTickets":100}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ticket", "mint", "--org", "acme.com", "--action", "comm.outbound",
		"--count", "100", "--note", "nightly send budget", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["MintActionTickets"], &vars)
	in := vars.Input
	if in["orgRef"] != "acme.com" || in["action"] != "comm.outbound" {
		t.Errorf("core fields wrong: %v", in)
	}
	if in["count"] != float64(100) {
		t.Errorf("--count should map, got %v", in["count"])
	}
	if in["note"] != "nightly send budget" {
		t.Errorf("--note should map, got %v", in["note"])
	}
	// Unset --app must be omitted.
	if v, present := in["appRef"]; present {
		t.Errorf("unset --app must be omitted, got %v", v)
	}
	var dto struct {
		Minted int `json:"minted"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json not valid: %v\n%s", err, out.String())
	}
	if dto.Minted != 100 {
		t.Errorf("output should report minted count, got %d", dto.Minted)
	}
}

func TestTicketMintRejectsNonPositiveCount(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ticket", "mint", "--org", "acme.com", "--count", "0", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "positive") {
		t.Fatalf("expected positive-count usage error, got %v", err)
	}
}

func TestTicketMintRejectsUnsupportedAction(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ticket", "mint", "--org", "acme.com", "--action", "comm.inbound",
		"--count", "5", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "unsupported --action") {
		t.Fatalf("expected unsupported-action usage error, got %v", err)
	}
}

func TestTicketLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ActionTickets": `{"data":{"actionTickets":{"total":1,"items":[` + ticketJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ticket", "ls", "--org", "acme.com", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["ActionTickets"], &vars)
	if vars["orgRef"] != "acme.com" {
		t.Errorf("--org should map to orgRef, got %v", vars["orgRef"])
	}
	got := out.String()
	// An unconsumed, non-expiring ticket reads as available.
	if !strings.Contains(got, "comm.outbound") || !strings.Contains(got, "available") {
		t.Errorf("ledger output missing action/status: %s", got)
	}
}

func TestTicketLsRequiresOrg(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ticket", "ls", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error when --org is missing")
	}
}
