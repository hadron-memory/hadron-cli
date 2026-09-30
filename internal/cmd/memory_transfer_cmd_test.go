package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const transferPreview = `{"data":{"transferMemoryOwnership":{"memoryId":"m1","oldUrn":"hrn:mem:alice:notes","newUrn":"hrn:mem:acme.com:notes","fromOwner":"alice-id","toOwner":"org-id","currentClass":"personal","targetClass":"knowledge","dependents":[{"kind":"nodes","count":3,"handling":"CARRY"}],"blockers":[],"canApply":true,"applied":false}}}`

func transferResponse(applied bool, blockers string) string {
	if blockers != "" {
		return `{"data":{"transferMemoryOwnership":{"memoryId":"m1","oldUrn":"hrn:mem:alice:notes","newUrn":"hrn:mem:acme.com:notes","fromOwner":"alice-id","toOwner":"org-id","currentClass":"personal","targetClass":"knowledge","dependents":[{"kind":"members","count":2,"handling":"REFUSE"}],"blockers":[` + blockers + `],"canApply":false,"applied":false}}}`
	}
	return strings.Replace(transferPreview, `"applied":false`, fmt.Sprintf(`"applied":%t`, applied), 1)
}

func transferGroupResponse(applied bool) string {
	return fmt.Sprintf(`{"data":{"transferMemoryOwnership":{"memoryId":"m2","oldUrn":"hrn:mem:old.com:notes","newUrn":"hrn:mem:acme.com:notes","fromOwner":"old-org-id","toOwner":"new-org-id","currentClass":"group","targetClass":"knowledge","dependents":[{"kind":"Memory members","count":2,"handling":"REMOVE"}],"blockers":[],"canApply":true,"applied":%t}}}`, applied)
}

func transferServer(t *testing.T, responses ...string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	requests := &[]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.OperationName != "TransferMemoryOwnership" {
			t.Errorf("unexpected operation %q", body.OperationName)
		}
		*requests = append(*requests, body.Variables)
		i := len(*requests) - 1
		if i >= len(responses) {
			t.Errorf("unexpected transfer call %d", i+1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responses[i]))
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func TestMemoryTransferPreviewOnly(t *testing.T) {
	server, requests := transferServer(t, transferPreview)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "transfer", "alice:notes", "--org", "acme.com", "--class", "knowledge", "--json", "--server", server.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(*requests) != 1 || (*requests)[0]["dryRun"] != true {
		t.Fatalf("preview must make one dry-run call: %v", *requests)
	}
	vars := (*requests)[0]
	if vars["memoryRef"] != "hrn:mem:alice:notes" || vars["orgRef"] != "acme.com" || vars["targetClass"] != "knowledge" {
		t.Errorf("preview vars: %v", vars)
	}
	if _, ok := vars["expectedNewUrn"]; ok {
		t.Errorf("preview must omit expectedNewUrn: %v", vars)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("JSON preview: %v\n%s", err, out)
	}
	if dto["applied"] != false || dto["newUrn"] != "hrn:mem:acme.com:notes" || len(dto["blockers"].([]any)) != 0 {
		t.Errorf("preview DTO: %v", dto)
	}
	if !strings.Contains(out.String(), `"blockers": []`) || !strings.Contains(out.String(), `"dependents": [`) {
		t.Errorf("JSON array shape: %s", out)
	}
}

func TestMemoryTransferApplyPinsPreviewedURN(t *testing.T) {
	server, requests := transferServer(t, transferGroupResponse(false), transferGroupResponse(true))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "transfer", "old.com:notes", "--org", "acme.com", "--class", "knowledge", "--reset-group-members", "--yes", "--json", "--server", server.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(*requests) != 2 {
		t.Fatalf("expected preview and apply calls, got %d", len(*requests))
	}
	if (*requests)[0]["dryRun"] != true || (*requests)[1]["dryRun"] != false || (*requests)[1]["expectedNewUrn"] != "hrn:mem:acme.com:notes" {
		t.Errorf("preview/apply guard: %v", *requests)
	}
	if (*requests)[0]["resetGroupMembers"] != true || (*requests)[1]["resetGroupMembers"] != true {
		t.Errorf("reset must be passed on both calls: %v", *requests)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil || dto["applied"] != true {
		t.Errorf("final JSON must be one applied document: %v, %s", err, out)
	}
}

func TestMemoryTransferBlockedPreviewKeepsTypedRefusal(t *testing.T) {
	blocked := transferResponse(false, `{"code":"MEMORY_TRANSFER_DEPENDENTS","message":"Pass resetGroupMembers: true to revoke members"}`)
	server, requests := transferServer(t, blocked)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "transfer", "alice:notes", "--org", "acme.com", "--yes", "--json", "--server", server.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Conflict || len(*requests) != 1 {
		t.Fatalf("blocked preview must exit 5 without apply, got %v, calls %d", err, len(*requests))
	}
	if !strings.Contains(out.String(), `"code": "MEMORY_TRANSFER_DEPENDENTS"`) {
		t.Errorf("blocker code lost: %s", out)
	}
	if !strings.Contains(out.String(), "--reset-group-members") || strings.Contains(out.String(), "resetGroupMembers: true") {
		t.Errorf("blocker remedy must name CLI flag: %s", out)
	}
}

func TestMemoryTransferApplyRequiresConfirmation(t *testing.T) {
	server, requests := transferServer(t, transferPreview)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "transfer", "alice:notes", "--org", "acme.com", "--apply", "--server", server.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Usage || len(*requests) != 1 {
		t.Fatalf("noninteractive --apply must preview but not apply: %v, calls %d", err, len(*requests))
	}
}

func TestMemoryTransferInteractiveApplyConfirmsAfterPreview(t *testing.T) {
	server, requests := transferServer(t, transferGroupResponse(false), transferGroupResponse(true))
	f, out, stderr := testFactoryTTY(t, "y\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "transfer", "old.com:notes", "--org", "acme.com", "--reset-group-members", "--apply", "--server", server.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("interactive apply: %v", err)
	}
	if len(*requests) != 2 || strings.Contains(out.String(), "Preview only") || !strings.Contains(out.String(), "Transferred ") {
		t.Errorf("stdout must contain only the final result: calls=%d, out=%q", len(*requests), out)
	}
	previewAt := strings.Index(stderr.String(), "Preview only")
	promptAt := strings.Index(stderr.String(), "revoke its old group memberships?")
	if previewAt < 0 || promptAt <= previewAt {
		t.Errorf("stderr must show preview before confirmation: %q", stderr)
	}
}

func TestMemoryTransferStaleApplyExitsConflict(t *testing.T) {
	stale := `{"errors":[{"message":"The target URN changed since the preview; run a new dryRun.","extensions":{"code":"STALE_TRANSFER_PREVIEW"}}]}`
	server, requests := transferServer(t, transferPreview, stale)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "transfer", "alice:notes", "--org", "acme.com", "--yes", "--server", server.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Conflict || len(*requests) != 2 {
		t.Fatalf("stale guarded apply must exit 5 without retry, got %v, calls %d", err, len(*requests))
	}
}

func TestMemoryTransferValidatesDestinationBeforeNetwork(t *testing.T) {
	for _, flags := range [][]string{{}, {"--org", "acme.com", "--user", "alice"}, {"--org", "acme.com", "--class", "app"}, {"--org="}, {"--user="}, {"--org", "acme.com", "--class="}} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"memory", "transfer", "alice:notes"}, flags...))
		if err := root.Execute(); exitCodeFor(err) != exitcode.Usage {
			t.Errorf("flags %v: want usage before network, got %v", flags, err)
		}
	}
}
