package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// cli#1949: each signing-preview row carries the server's agentUrn and
// workerUrn. They are display fields printed exactly as served (no URN is built
// or re-canonicalised here), and an absent one is an explicit JSON null.

const (
	urnAgentA  = "hrn:agent:author.com:planner"
	urnWorkerA = "hrn:worker:acme.com:team:ada"
	urnAgentB  = "hrn:agent:author.com:reviewer"
)

// urnRows serves three rows: a worker row (both URNs), an install-level row
// (agentUrn only, workerUrn null) and a row where the server derived neither.
func urnRows() string {
	row := func(n int, agent, worker string) string {
		return fmt.Sprintf(`{"appId":"app-%d","appUrn":"hrn:app:acme.com:a%d","agentId":"agt-%d","agentName":"agent-%d","workerId":null,"workerName":null,"compiledNodeId":"cn-%d","compiledNodeUrn":null,"servedRevSeq":%d,"state":"UNSIGNED_STOPS","changesToRevSeq":null,"agentUrn":%s,"workerUrn":%s}`,
			n, n, n, n, n, n, agent, worker)
	}
	rows := strings.Join([]string{
		row(1, `"`+urnAgentA+`"`, `"`+urnWorkerA+`"`),
		row(2, `"`+urnAgentB+`"`, `null`),
		row(3, `null`, `null`),
	}, ",")
	return `{"data":{"orgBootPromptSigningPreview":{"enforced":false,"deploymentSigns":true,"total":3,"hasMore":false,"endCursor":null,"previewToken":"tok","rows":[` + rows + `]}}}`
}

func urnFake(t *testing.T) *sigFake {
	return newSigFake(t, func(op string, _ int, _ map[string]any) string {
		if op == "SetOrgBootPromptSigning" {
			return setResp(true, true, true)
		}
		return urnRows()
	})
}

func checkURNRows(t *testing.T, rows []any) {
	t.Helper()
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	want := [][2]any{{urnAgentA, urnWorkerA}, {urnAgentB, nil}, {nil, nil}}
	for i, r := range rows {
		row := r.(map[string]any)
		for j, k := range []string{"agentUrn", "workerUrn"} {
			v, ok := row[k]
			if !ok {
				t.Errorf("row %d lacks %q (null must be an explicit key, not omitted)", i, k)
				continue
			}
			if v != want[i][j] {
				t.Errorf("row %d %s = %v, want %v", i, k, v, want[i][j])
			}
		}
	}
}

func TestOrgSigningPreviewJSONCarriesAgentAndWorkerURNs(t *testing.T) {
	fake := urnFake(t)
	f, out := testFactory(t)
	if code := runSigning(t, f, fake, "org", "signing", "preview", "acme.com", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got struct{ Rows []any }
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	checkURNRows(t, got.Rows)
}

func TestOrgSigningOnJSONEmbedsAgentAndWorkerURNs(t *testing.T) {
	fake := urnFake(t)
	f, out := testFactory(t)
	if code := runSigning(t, f, fake, "org", "signing", "on", "acme.com", "--yes", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		Preview struct{ Rows []any }
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("stdout must be one JSON document: %v\n%s", err, out)
	}
	checkURNRows(t, got.Preview.Rows)
}

func TestOrgSigningPreviewTextPrintsPresentURNsAndOmitsAbsent(t *testing.T) {
	fake := urnFake(t)
	f, out := testFactory(t)
	if code := runSigning(t, f, fake, "org", "signing", "preview", "acme.com"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	s := out.String()
	for _, want := range []string{urnAgentA, urnWorkerA, urnAgentB} {
		if !strings.Contains(s, want) {
			t.Errorf("text lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "null") || strings.Contains(s, "<nil>") {
		t.Errorf("an absent URN must be omitted, not printed:\n%s", s)
	}
}
