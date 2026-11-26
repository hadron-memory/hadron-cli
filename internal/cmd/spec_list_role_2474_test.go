package cmd

import (
	"strings"
	"testing"
)

// cli#2474 (follow-ups to #2472, source #684 Slice A): refusal pins, help and
// test scope for `spec list --role`.

const specRoleBoom2474 = `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`

func specRoleStates2474(filters []map[string]any) map[string]bool {
	states := map[string]bool{}
	for _, f := range filters {
		if s, ok := f["approvalState"].(string); ok {
			states[s] = true
		}
	}
	return states
}

// Rook F1: --unapproved reads NOT_APPROVED first and SUPERSEDED only if that
// succeeded. A failing first partition must end the command: non-zero exit, no
// rows, and the second partition never asked (it could otherwise succeed and
// leave a silently partial list).
func TestSpecListRoleUnapprovedFirstPartitionFailureEndsIt2474(t *testing.T) {
	out, filters, _, err := runSpecListRole684State(t, "spec.rule", []string{"--unapproved"}, specRoleBoom2474, "NOT_APPROVED")
	if err == nil || exitCodeFor(err) == 0 {
		t.Fatalf("a failed first partition must fail the command: err=%v", err)
	}
	if out != "" {
		t.Fatalf("partial output after a failed first partition: %q", out)
	}
	if states := specRoleStates2474(filters); !states["NOT_APPROVED"] || states["SUPERSEDED"] {
		t.Fatalf("the second partition ran after the first failed: %v", states)
	}
}

// Gemma F2: only the SUPERSEDED partition fails. The command fails and prints
// nothing, not the NOT_APPROVED rows it had already read.
func TestSpecListRoleUnapprovedSecondPartitionFailureShowsNoRows2474(t *testing.T) {
	out, filters, _, err := runSpecListRole684State(t, "spec.rule", []string{"--unapproved"}, specRoleBoom2474, "SUPERSEDED")
	if err == nil || exitCodeFor(err) == 0 {
		t.Fatalf("a failed second partition must fail the command: err=%v", err)
	}
	if out != "" {
		t.Fatalf("partial rows from the first partition were shown: %q", out)
	}
	if states := specRoleStates2474(filters); !states["NOT_APPROVED"] || !states["SUPERSEDED"] {
		t.Fatalf("both partitions should have been asked: %v", states)
	}
}

// Gemma F7: with no -m and no --prefix the wire filter is the role alone: no
// memory scope and no location prefix.
func TestSpecListRoleWithoutScopeSendsTheRoleAlone2474(t *testing.T) {
	out, filters, _, err := runSpecListRole684(t, "spec.rule", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(filters) == 0 {
		t.Fatal("no request reached the server")
	}
	for _, f := range filters {
		if f["role"] != "spec.rule" {
			t.Fatalf("role lost: %v", f)
		}
		for _, key := range []string{"memoryRefs", "locPrefix", "tags", "approvalState", "minted"} {
			if _, has := f[key]; has {
				t.Fatalf("an unscoped --role list sent %q: %v", key, f)
			}
		}
	}
	if !strings.Contains(out, "msg:010:02") {
		t.Fatalf("no rows listed: %s", out)
	}
}

// Gemma F4: an older server's validation error is wrapped in a clear message
// instead of reaching the user raw. It stays a refusal: non-zero exit, nothing
// listed, no fallback request.
func TestSpecListRoleOlderServerIsExplained2474(t *testing.T) {
	skew := `{"errors":[{"message":"Field \"role\" is not defined by type \"NodeFilter\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	out, filters, _, err := runSpecListRole684(t, "spec.rule", nil, skew)
	if err == nil || exitCodeFor(err) == 0 || out != "" || len(filters) != 1 {
		t.Fatalf("an older server must be a refusal with no fallback: err=%v out=%q filters=%d", err, out, len(filters))
	}
	if msg := err.Error(); !strings.Contains(msg, "--role") || !strings.Contains(msg, "does not support") || strings.Contains(msg, "is not defined by type") {
		t.Fatalf("the refusal is not the explained one: %q", msg)
	}
	// Any other validation failure is not mistaken for it.
	other := `{"errors":[{"message":"Field \"limit\" is not defined by type \"NodeFilter\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	_, _, _, err = runSpecListRole684(t, "spec.rule", nil, other)
	if err == nil || strings.Contains(err.Error(), "does not support --role") {
		t.Fatalf("an unrelated validation error was reported as a missing --role: %v", err)
	}
}

// Gemma F5: --role lists whatever nodes carry the role, spec or not; the help
// says so.
func TestSpecListRoleHelpSaysTheValueNeedNotBeASpecRole2474(t *testing.T) {
	_, out, err := runTeamTask(t, nil, "spec", "ls", "--help")
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(flat, "need not be a spec role") {
		t.Fatalf("the help does not say the role value need not be a spec role: %s", out)
	}
}
