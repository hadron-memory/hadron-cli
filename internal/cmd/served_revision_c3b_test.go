package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestC3bServedRecordNullableAndExplicit(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		op, body string
	}{
		{[]string{"team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--json"}, "WorkerServedPrompt", `{"data":{"worker":{"servedPrompt":null}}}`},
		{[]string{"run", "served-prompt", "run_123", "--json"}, "RunServedPrompt", `{"data":{"appRun":{"servedPrompt":null}}}`},
	} {
		t.Run(tc.op, func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{tc.op: tc.body}, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 || !strings.Contains(out, `"servedPrompt": null`) {
				t.Fatalf("wrong nullable record: %v %s", *calls, out)
			}
			c := c2Mutation(t, calls, tc.op)
			if c.Vars["ref"] != tc.args[len(tc.args)-2] {
				t.Fatalf("wrong ref: %s", c.RawVars)
			}
		})
	}
}

const c3bRecord = `{"id":"rec1","kind":"BIND","ordinal":1,"pointerLevel":"WORKER","compiledNodeId":"compiled1","compiledContentHash":"recorded-hash","servedRevSeq":7,"provenanceRevSeq":5,"servedAt":"2026-10-28T09:00:00Z","variables":{"worker.signature":"recorded signature"},"pruned":true,"provenance":{"items":[{"nodeId":"source1","revSeq":3,"hash":"source-hash","pruned":false}],"total":2}}`

func c3bWorkerRecord(record string) string {
	return `{"data":{"worker":{"servedPrompt":` + record + `}}}`
}

func TestC3bRecordedFieldsAndNoSourceReads(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"WorkerServedPrompt": c3bWorkerRecord(c3bRecord)}, "team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("extra reads: %v", *calls)
	}
	for _, s := range []string{`"servedRevSeq": 7`, `"provenanceRevSeq": 5`, `"pruned": true`, `"pruned": false`, `"compiledContentHash": "recorded-hash"`, `"worker.signature": "recorded signature"`} {
		if !strings.Contains(out, s) {
			t.Fatalf("lost recorded value %s: %s", s, out)
		}
	}
	c := c2Mutation(t, calls, "WorkerServedPrompt")
	if c.Vars["limit"] != float64(50) || c.Vars["offset"] != float64(0) {
		t.Fatalf("bad page variables: %s", c.RawVars)
	}
	for _, s := range []string{"content(", "edges(", "bootPromptNode", "promptOverride", "node {", "loc\n"} {
		if strings.Contains(c.Query, s) {
			t.Fatalf("query reads source/live state: %s", c.Query)
		}
	}
}

func TestC3bExplicitZeroCountsAndEmptyPage(t *testing.T) {
	record := strings.Replace(c3bRecord, `[{"nodeId":"source1","revSeq":3,"hash":"source-hash","pruned":false}]`, `[]`, 1)
	calls, out, err := runTeamTask(t, map[string]string{"RunServedPrompt": `{"data":{"appRun":{"servedPrompt":` + record + `}}}`}, "run", "served-prompt", "run_123", "--limit", "0", "--json")
	if err != nil {
		t.Fatal(err)
	}
	c := c2Mutation(t, calls, "RunServedPrompt")
	if c.Vars["limit"] != float64(0) || !strings.Contains(out, `"items": []`) || !strings.Contains(out, `"total": 2`) {
		t.Fatalf("zero lost: %s %s", c.RawVars, out)
	}
}

func TestC3bAllProvenanceBuffersAndChecksRecord(t *testing.T) {
	second := strings.Replace(c3bRecord, "source1", "source2", 1)
	for _, tc := range []struct {
		name, second string
		wantCode     int
	}{
		{"complete", c3bWorkerRecord(second), 0},
		{"unavailable", c3bWorkerRecord("null"), 1},
		{"changed total", c3bWorkerRecord(strings.Replace(second, `"total":2`, `"total":3`, 1)), 1},
		{"null items", c3bWorkerRecord(strings.Replace(second, `[{"nodeId":"source2","revSeq":3,"hash":"source-hash","pruned":false}]`, `null`, 1)), 1},
		{"changed revision", c3bWorkerRecord(strings.Replace(second, `"servedRevSeq":7`, `"servedRevSeq":8`, 1)), 1},
		{"changed record", c3bWorkerRecord(strings.Replace(second, `"id":"rec1"`, `"id":"rec2"`, 1)), 1},
		{"empty page", c3bWorkerRecord(strings.Replace(second, `[{"nodeId":"source2","revSeq":3,"hash":"source-hash","pruned":false}]`, `[]`, 1)), 1},
		{"null provenance", c3bWorkerRecord(strings.Replace(second, `{"items":[{"nodeId":"source2","revSeq":3,"hash":"source-hash","pruned":false}],"total":2}`, `null`, 1)), 1},
		{"provider error", gqlErrorJSON("FORBIDDEN"), 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			responses := map[string]string{}
			count := 0
			srv, calls := attnServerHook(t, responses, func(op string) {
				if op != "WorkerServedPrompt" {
					t.Errorf("unexpected operation %s", op)
					return
				}
				count++
				if count > 2 {
					responses[op] = gqlErrorJSON("FORBIDDEN")
					return
				}
				if count == 1 {
					responses[op] = c3bWorkerRecord(c3bRecord)
				} else {
					responses[op] = tc.second
				}
			})
			setTeamBindingServer(t, srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"--server", srv.URL, "team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--limit", "1", "--all", "--json"})
			err := root.Execute()
			if exitCodeFor(err) != tc.wantCode {
				t.Fatalf("got %v", err)
			}
			if count != 2 || len(*calls) != 2 || (*calls)[1].Vars["offset"] != float64(1) {
				t.Fatalf("bad paging: %v", *calls)
			}
			if tc.wantCode != 0 && strings.Contains(out.String(), "recorded-hash") {
				t.Fatalf("partial result: %s", out.String())
			}
			if tc.wantCode == 0 && (!strings.Contains(out.String(), "source1") || !strings.Contains(out.String(), "source2")) {
				t.Fatalf("truncated: %s", out.String())
			}
		})
	}
}

func TestC3bInvalidArgsBeforeAnyRequest(t *testing.T) {
	for _, extra := range [][]string{{"--limit", "-1"}, {"--limit", "201"}, {"--offset", "-1"}, {"--all", "--offset", "0"}, {"--all", "--limit", "0"}} {
		args := append([]string{"run", "served-prompt", "run_123"}, extra...)
		calls, _, err := runTeamTask(t, nil, args...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("expected Usage/no calls: %v %v", err, *calls)
		}
	}
	calls, _, err := runTeamTask(t, nil, "team", "worker", "served-prompt", "Iris")
	if exitCodeFor(err) != 2 || len(*calls) != 0 {
		t.Fatalf("name must not use ambient binding: %v %v", err, *calls)
	}
}

func TestC3bPageBoundsAndHumanRendering(t *testing.T) {
	item := `{"nodeId":"source1","revSeq":3,"hash":"source-hash","pruned":false}`
	for _, tc := range []struct {
		name, record string
		flags        []string
	}{
		{"over limit", strings.Replace(c3bRecord, `[`+item+`]`, `[`+item+`,`+item+`]`, 1), []string{"--limit", "1"}},
		{"beyond remaining total", strings.Replace(c3bRecord, `[`+item+`]`, `[`+item+`,`+item+`]`, 1), []string{"--offset", "1", "--limit", "2"}},
		{"at total", c3bRecord, []string{"--offset", "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--json"}, tc.flags...)
			_, out, err := runTeamTask(t, map[string]string{"WorkerServedPrompt": c3bWorkerRecord(tc.record)}, args...)
			if exitCodeFor(err) != 1 || out != "" {
				t.Fatalf("invalid page accepted: %v %s", err, out)
			}
		})
	}
	for _, record := range []string{"null", c3bRecord} {
		_, out, err := runTeamTask(t, map[string]string{"WorkerServedPrompt": c3bWorkerRecord(record)}, "team", "worker", "served-prompt", "hrn:worker:acme.com:iris")
		if err != nil {
			t.Fatal(err)
		}
		if record == "null" {
			if !strings.Contains(out, "unavailable") {
				t.Fatalf("missing null wording: %s", out)
			}
		} else {
			for _, want := range []string{"record: rec1", "ordinal: 1; pointer: WORKER", "served revision: 7; provenance revision: 5", "recorded signature", "source1", "source-hash", "false"} {
				if !strings.Contains(out, want) {
					t.Fatalf("human value %q missing: %s", want, out)
				}
			}
		}
	}
	// Record text stays safe even when recorded variables contain terminal controls.
	var record map[string]any
	if err := json.Unmarshal([]byte(c3bRecord), &record); err != nil {
		t.Fatal(err)
	}
	record["variables"] = map[string]any{"value": "control\x1b[31m"}
	raw, _ := json.Marshal(record)
	_, out, err := runTeamTask(t, map[string]string{"WorkerServedPrompt": c3bWorkerRecord(string(raw))}, "team", "worker", "served-prompt", "hrn:worker:acme.com:iris")
	if err != nil || strings.ContainsAny(out, "\x1b\r") {
		t.Fatalf("unsafe output: %v %q", err, out)
	}
}

func TestC3bAllHumanPrintsCompletedProvenance(t *testing.T) {
	writeTeamBinding(t)
	responses := map[string]string{}
	requests := 0
	srv, calls := attnServerHook(t, responses, func(op string) {
		requests++
		if requests > 2 {
			responses[op] = gqlErrorJSON("FORBIDDEN")
			return
		}
		record := c3bRecord
		if requests == 2 {
			record = strings.Replace(record, "source1", "source2", 1)
		}
		responses[op] = c3bWorkerRecord(record)
	})
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--all", "--limit", "1"})
	err := root.Execute()
	if err != nil || len(*calls) != 2 || !strings.Contains(out.String(), "source1") || !strings.Contains(out.String(), "source2") || !strings.Contains(out.String(), "provenance: 2 of 2") {
		t.Fatalf("wrong human all output: %v %s %v", err, out, *calls)
	}
}
