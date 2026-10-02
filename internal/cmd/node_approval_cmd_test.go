package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestNodeMintDryRunReportsTamperAndNeverWrites(t *testing.T) {
	var calls []bool
	gql := newVarServer(t, func(op string, raw json.RawMessage) string {
		if op != "MintMemoryNodes" {
			t.Errorf("unexpected operation %s", op)
			return `{}`
		}
		var v struct {
			MemoryRef string `json:"memoryRef"`
			DryRun    bool   `json:"dryRun"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		if v.MemoryRef != "hrn:mem:acme.com:kb" {
			t.Errorf("memory = %q", v.MemoryRef)
		}
		calls = append(calls, v.DryRun)
		return `{"data":{"mintMemoryNodes":{"memoryId":"m1","dryRun":true,"minted":false,"mintedCount":0,"mintLocs":["cor:one"],"blockers":[{"kind":"TAMPERED","rule":null,"loc":"cor:one","targetLoc":null,"message":"approval hash differs"}],"staleAbstracts":[],"staleAbstractsBlock":false,"openQuestions":[]}}}`
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "mint", "-m", "acme.com::kb", "--dry-run", "--json", "--server", gql.URL})
	if code := exitCodeFor(root.Execute()); code != exitcode.Conflict {
		t.Fatalf("exit = %d; output %s", code, out.String())
	}
	if len(calls) != 1 || !calls[0] {
		t.Errorf("mint calls = %v", calls)
	}
	if !strings.Contains(out.String(), `"TAMPERED"`) {
		t.Errorf("tamper blocker absent: %s", out.String())
	}
}

func TestNodeMintYesReturnsActualCountAndLocs(t *testing.T) {
	var calls []bool
	gql := newVarServer(t, func(op string, raw json.RawMessage) string {
		if op != "MintMemoryNodes" {
			t.Errorf("unexpected operation %s", op)
			return `{}`
		}
		var v struct {
			DryRun bool `json:"dryRun"`
		}
		_ = json.Unmarshal(raw, &v)
		calls = append(calls, v.DryRun)
		if v.DryRun {
			return `{"data":{"mintMemoryNodes":{"memoryId":"m1","dryRun":true,"minted":false,"mintedCount":0,"mintLocs":["cor:one"],"blockers":[],"staleAbstracts":[],"staleAbstractsBlock":false,"openQuestions":[]}}}`
		}
		return `{"data":{"mintMemoryNodes":{"memoryId":"m1","dryRun":false,"minted":true,"mintedCount":1,"mintLocs":["cor:one"],"blockers":[],"staleAbstracts":[],"staleAbstractsBlock":false,"openQuestions":[]}}}`
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "mint", "-m", "hrn:mem:acme.com:kb", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !calls[0] || calls[1] {
		t.Errorf("mint calls = %v", calls)
	}
	var got struct {
		MintedCount int      `json:"mintedCount"`
		MintLocs    []string `json:"mintLocs"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if got.MintedCount != 1 || len(got.MintLocs) != 1 || got.MintLocs[0] != "cor:one" {
		t.Errorf("mint report = %+v", got)
	}
}

func TestSpecListUnmintedSendsFalseBeforePaging(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "list", "-m", specMem, "--unminted", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var vars struct {
		Filter struct {
			Minted *bool `json:"minted"`
		} `json:"filter"`
	}
	if err := json.Unmarshal(captured["FindNodes"], &vars); err != nil {
		t.Fatal(err)
	}
	if vars.Filter.Minted == nil || *vars.Filter.Minted {
		t.Errorf("unminted filter must send false, got %+v", vars.Filter)
	}
}

func TestSpecListReportsApprovalAndMintState(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":                 `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec"]`) + `]}}`,
		"NodeLiveRevisionsApproved": `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[{"id":"id-msg:010:02","revision":3,"authorship":null,"contentValidation":null,"approvalStatus":{"state":"APPROVED","approval":{"revision":3,"approvedAt":"2026-10-01T23:00:00Z","approvedBy":null,"approvedByInfo":"app:abc","hash":"aaaa"}}}]}}}`,
		"NodeLiveRevisionsMinted":   `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[{"id":"id-msg:010:02","revision":3,"authorship":null,"contentValidation":null,"approvalStatus":{"state":"APPROVED","approval":{"revision":3,"approvedAt":"2026-10-01T23:00:00Z","approvedBy":null,"approvedByInfo":"app:abc","hash":"aaaa"}},"mintStatus":{"minted":true,"mintedAt":"2026-10-01T23:01:00Z","mintedBy":null,"mintedByInfo":"app:abc","revision":3,"hash":"aaaa"}}]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "list", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got []struct {
		ApprovalStatus struct {
			State string `json:"state"`
		} `json:"approvalStatus"`
		MintStatus struct {
			Minted   bool `json:"minted"`
			Revision *int `json:"revision"`
		} `json:"mintStatus"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ApprovalStatus.State != "APPROVED" || !got[0].MintStatus.Minted || got[0].MintStatus.Revision == nil || *got[0].MintStatus.Revision != 3 {
		t.Errorf("spec statuses = %+v", got)
	}
}

func TestSpecListKeepsApprovalOnServerBeforeMintStatus(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":                 `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec"]`) + `]}}`,
		"NodeLiveRevisionsApproved": `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[{"id":"id-msg:010:02","revision":3,"authorship":null,"contentValidation":null,"approvalStatus":{"state":"NOT_APPROVED","approval":null}}]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "list", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got []struct {
		ApprovalStatus struct {
			State string `json:"state"`
		} `json:"approvalStatus"`
		MintStatus *struct {
			Minted bool `json:"minted"`
		} `json:"mintStatus"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ApprovalStatus.State != "NOT_APPROVED" || got[0].MintStatus != nil {
		t.Errorf("PR-1 status fallback = %+v", got)
	}
}

func TestNodeApprovePinsTheRevisionAndKeepsTheServerReceipt(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":  resolveNodeJSON,
		"ApproveNode": `{"data":{"approveNode":{"nodeId":"n1","urn":"hrn:node:acme.com:kb:findings:flaky-ci","loc":"findings:flaky-ci","approval":{"revision":4,"approvedAt":"2026-10-01T23:00:00Z","approvedBy":{"handle":"ada","urn":"hrn:user:ada"},"approvedByInfo":null,"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "approve", nodeURN, "--revision", "4", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var vars struct {
		NodeRef  string `json:"nodeRef"`
		Revision *int   `json:"revision"`
	}
	if err := json.Unmarshal(captured["ApproveNode"], &vars); err != nil {
		t.Fatal(err)
	}
	if vars.NodeRef != "n1" || vars.Revision == nil || *vars.Revision != 4 {
		t.Errorf("approve variables = %+v", vars)
	}
	var dto struct {
		Approval struct {
			Revision   int    `json:"revision"`
			ApprovedAt string `json:"approvedAt"`
			Hash       string `json:"hash"`
			ApprovedBy struct {
				Handle string `json:"handle"`
			} `json:"approvedBy"`
		} `json:"approval"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Approval.Revision != 4 || dto.Approval.ApprovedBy.Handle != "ada" || len(dto.Approval.Hash) != 64 {
		t.Errorf("approval receipt = %+v", dto)
	}
}

func TestMemoryApproveAllSendsMemoryAndReturnsCompleteReceipt(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ApproveMemoryNodes": `{"data":{"approveMemoryNodes":{"approvedCount":1,"alreadyApprovedCount":2,"skippedPlaceholders":["cor:next"],"approved":[{"nodeId":"n1","urn":"hrn:node:acme.com:kb:cor:one","loc":"cor:one","approval":{"revision":3,"approvedAt":"2026-10-01T23:00:00Z","approvedBy":null,"approvedByInfo":"app:abc","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "approve-all", "-m", "acme.com::kb", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var vars struct {
		MemoryRef string `json:"memoryRef"`
	}
	if err := json.Unmarshal(captured["ApproveMemoryNodes"], &vars); err != nil {
		t.Fatal(err)
	}
	if vars.MemoryRef != "hrn:mem:acme.com:kb" {
		t.Errorf("memoryRef = %q", vars.MemoryRef)
	}
	var dto struct {
		ApprovedCount        int `json:"approvedCount"`
		AlreadyApprovedCount int `json:"alreadyApprovedCount"`
		Approved             []struct {
			Approval struct {
				ApprovedByInfo *string `json:"approvedByInfo"`
			} `json:"approval"`
		} `json:"approved"`
		SkippedPlaceholders []string `json:"skippedPlaceholders"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.ApprovedCount != 1 || dto.AlreadyApprovedCount != 2 || len(dto.Approved) != 1 || len(dto.SkippedPlaceholders) != 1 {
		t.Errorf("bulk approval receipt = %+v", dto)
	}
	if dto.Approved[0].Approval.ApprovedByInfo == nil || *dto.Approved[0].Approval.ApprovedByInfo != "app:abc" {
		t.Errorf("non-user approver lost: %+v", dto.Approved[0])
	}
}

func TestNodeVerifyKeepsSupersededSeparateFromTampered(t *testing.T) {
	for _, tc := range []struct {
		state string
		code  int
	}{
		{"INTACT", 0}, {"SUPERSEDED", exitcode.Conflict}, {"TAMPERED", exitcode.Conflict}, {"NOT_APPROVED", exitcode.Conflict},
	} {
		t.Run(tc.state, func(t *testing.T) {
			gql := fakeGraphQL(t, map[string]string{
				"ResolveUrn": resolveNodeJSON,
				"VerifyNode": `{"data":{"verifyNode":{"nodeId":"n1","urn":"hrn:node:acme.com:kb:findings:flaky-ci","loc":"findings:flaky-ci","state":"` + tc.state + `","revision":5,"approvedRevision":4,"expectedHash":"aaaa","actualHash":"bbbb"}}}`,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "verify", nodeURN, "--json", "--server", gql.URL})
			if got := exitCodeFor(root.Execute()); got != tc.code {
				t.Errorf("exit = %d, want %d", got, tc.code)
			}
			var dto struct {
				State string `json:"state"`
			}
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if dto.State != tc.state {
				t.Errorf("server state = %q, want %q", dto.State, tc.state)
			}
		})
	}
}

func TestNodeGetKeepsApprovalFromAfterProbe(t *testing.T) {
	probes := 0
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "ResolveUrn":
			return resolveNodeJSON
		case "GetNode":
			return `{"data":{"node":` + nodeDetailJSON + `}}`
		case "NodeLiveRevisionsApproved":
			probes++
			state := `{"state":"NOT_APPROVED","approval":null}`
			if probes == 2 {
				state = `{"state":"APPROVED","approval":{"revision":4,"approvedAt":"2026-10-01T23:00:00Z","approvedBy":null,"approvedByInfo":"app:abc","hash":"aaaa"}}`
			}
			return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[{"id":"n1","revision":4,"authorship":null,"contentValidation":null,"approvalStatus":` + state + `}]}}}`
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", nodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if probes != 2 {
		t.Fatalf("approval probes = %d, want 2", probes)
	}
	var dto struct {
		Revision       *int `json:"revision"`
		ApprovalStatus struct {
			State    string `json:"state"`
			Approval struct {
				Revision int `json:"revision"`
			} `json:"approval"`
		} `json:"approvalStatus"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Revision == nil || *dto.Revision != 4 || dto.ApprovalStatus.State != "APPROVED" || dto.ApprovalStatus.Approval.Revision != 4 {
		t.Errorf("after-probe approval not paired with node: %+v", dto)
	}
}

func TestNodeGetKeepsMintFromAfterProbe(t *testing.T) {
	probes := 0
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "ResolveUrn":
			return resolveNodeJSON
		case "GetNode":
			return `{"data":{"node":` + nodeDetailJSON + `}}`
		case "NodeLiveRevisionsMinted":
			probes++
			mint := `{"minted":false,"mintedAt":null,"mintedBy":null,"mintedByInfo":null,"revision":null,"hash":null}`
			if probes == 2 {
				mint = `{"minted":true,"mintedAt":"2026-10-01T23:00:00Z","mintedBy":null,"mintedByInfo":"app:abc","revision":4,"hash":"aaaa"}`
			}
			return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[{"id":"n1","revision":4,"authorship":null,"contentValidation":null,"approvalStatus":{"state":"APPROVED","approval":null},"mintStatus":` + mint + `}]}}}`
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", nodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if probes != 2 {
		t.Fatalf("mint probes = %d, want 2", probes)
	}
	var dto struct {
		Revision   *int `json:"revision"`
		MintStatus struct {
			Minted       bool    `json:"minted"`
			Revision     *int    `json:"revision"`
			MintedByInfo *string `json:"mintedByInfo"`
		} `json:"mintStatus"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Revision == nil || *dto.Revision != 4 || !dto.MintStatus.Minted || dto.MintStatus.Revision == nil || *dto.MintStatus.Revision != 4 || dto.MintStatus.MintedByInfo == nil || *dto.MintStatus.MintedByInfo != "app:abc" {
		t.Errorf("after-probe mint not paired with node: %+v", dto)
	}
}
