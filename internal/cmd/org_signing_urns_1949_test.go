package cmd

import (
	"encoding/json"
	"fmt"
	"slices"
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

	workerNameA = "ada"
)

// urnRows serves three rows: a worker row (a real worker id and name, both
// URNs), an install-level row (agentUrn only; no worker, so workerUrn is null as
// the server derives it) and a row where the server derived neither.
func urnRows() string {
	row := func(n int, agent, worker, workerID, workerName string) string {
		return fmt.Sprintf(`{"appId":"app-%d","appUrn":"hrn:app:acme.com:a%d","agentId":"agt-%d","agentName":"agent-%d","workerId":%s,"workerName":%s,"compiledNodeId":"cn-%d","compiledNodeUrn":null,"servedRevSeq":%d,"state":"UNSIGNED_STOPS","changesToRevSeq":null,"agentUrn":%s,"workerUrn":%s}`,
			n, n, n, n, workerID, workerName, n, n, agent, worker)
	}
	rows := strings.Join([]string{
		row(1, `"`+urnAgentA+`"`, `"`+urnWorkerA+`"`, `"wrk-1"`, `"`+workerNameA+`"`),
		row(2, `"`+urnAgentB+`"`, `null`, `null`, `null`),
		row(3, `null`, `null`, `null`, `null`),
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
	lines := strings.Split(out.String(), "\n")
	var header string
	byApp := map[string]string{}
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "APP"):
			header = l
		case strings.Contains(l, "hrn:app:acme.com:a"):
			byApp[strings.Fields(l)[0]] = l
		}
	}
	for _, app := range []string{"a1", "a2", "a3"} {
		if byApp["hrn:app:acme.com:"+app] == "" {
			t.Fatalf("row %s is missing from the table:\n%s", app, out.String())
		}
	}
	for _, col := range []string{"AGENT URN", "WORKER URN"} {
		if !strings.Contains(header, col) {
			t.Errorf("header lacks %q: %q", col, header)
		}
	}
	// Row 1: the worker's name sits in the WORKER cell, beside its workerUrn.
	r1 := strings.Fields(byApp["hrn:app:acme.com:a1"])
	if !slices.Contains(r1, workerNameA) || !slices.Contains(r1, urnAgentA) || !slices.Contains(r1, urnWorkerA) {
		t.Errorf("worker row lacks name/URNs: %q", byApp["hrn:app:acme.com:a1"])
	}
	// Row 2: an install row prints (install) and agentUrn only.
	r2 := byApp["hrn:app:acme.com:a2"]
	if !strings.Contains(r2, "(install)") || !strings.Contains(r2, urnAgentB) || strings.Contains(r2, "hrn:worker:") {
		t.Errorf("install row wrong: %q", r2)
	}
	// Row 3: neither URN, and nothing placeholder-like in the row.
	r3 := byApp["hrn:app:acme.com:a3"]
	if strings.Contains(r3, "hrn:agent:") || strings.Contains(r3, "hrn:worker:") || strings.Contains(r3, "null") || strings.Contains(r3, "<nil>") {
		t.Errorf("absent URNs must be omitted, not printed: %q", r3)
	}
}
