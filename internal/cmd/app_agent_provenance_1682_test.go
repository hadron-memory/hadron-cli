package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

const provenanceApp1682 = "hrn:app:acme.com:support"
const missingPreview1682 = `{"errors":[{"message":"Cannot query field \"bootPromptServe\" on type \"AppAgent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
const missingMethod1682 = `{"errors":[{"message":"Cannot query field \"installMethod\" on type \"AppAgent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`

type provenanceStep1682 struct{ op, reply string }

// The two-call case is synthetic exact-capability evidence, not a claim that a
// stock pre-2708 provider supports the legacy installation/provenance floor.
func provenanceSteps1682(reply string, legacy bool) []provenanceStep1682 {
	if legacy {
		return []provenanceStep1682{{"AppAgentEffectiveRoster", missingPreview1682}, {"AppAgentInstallationRoster", reply}}
	}
	return []provenanceStep1682{{"AppAgentEffectiveRoster", reply}}
}

type provenanceRequest1682 struct {
	OperationName string         `json:"operationName"`
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables"`
}

func checkProvenanceRequests1682(got []provenanceRequest1682, steps []provenanceStep1682) error {
	if len(got) != len(steps) {
		return fmt.Errorf("requests %d want %d", len(got), len(steps))
	}
	for i, r := range got {
		doc := gen.AppAgentEffectiveRoster_Operation
		if steps[i].op == "AppAgentInstallationRoster" {
			doc = gen.AppAgentInstallationRoster_Operation
		}
		if r.OperationName != steps[i].op || r.Query != doc || !reflect.DeepEqual(r.Variables, map[string]any{"appRef": provenanceApp1682}) {
			return fmt.Errorf("request %d operation/document/variables mismatch: %#v", i, r)
		}
	}
	return nil
}

// A strict, ordered recorder: no unstubbedDefault, operation aliases, or
// per-operation map that could collapse a repeated request. Mutex guards even
// unexpected handler access; all assertions occur in the test goroutine.
func runProvenance1682(t *testing.T, steps []provenanceStep1682, asJSON bool) (string, string, int, error) {
	t.Helper()
	var mu sync.Mutex
	var requests []provenanceRequest1682
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req provenanceRequest1682
		err := json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		i := len(requests)
		requests = append(requests, req)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || err != nil || i >= len(steps) || req.OperationName != steps[i].op {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected request"}]}`))
			return
		}
		_, _ = w.Write([]byte(steps[i].reply))
	}))
	defer srv.Close()
	f, out := testFactory(t)
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	args := []string{"--server", srv.URL, "app", "agent", "list", provenanceApp1682}
	if asJSON {
		args = append(args, "--json")
	}
	root.SetArgs(args)
	err := root.Execute()
	code := 0
	if err != nil {
		code = renderError(f, err)
	}
	mu.Lock()
	got := append([]provenanceRequest1682(nil), requests...)
	mu.Unlock()
	if err := checkProvenanceRequests1682(got, steps); err != nil {
		t.Fatal(err)
	}
	return out.String(), stderr.String(), code, err
}

func TestAppAgentProvenanceRequestControls1682(t *testing.T) {
	steps := provenanceSteps1682(`{}`, true)
	valid := []provenanceRequest1682{
		{"AppAgentEffectiveRoster", gen.AppAgentEffectiveRoster_Operation, map[string]any{"appRef": provenanceApp1682}},
		{"AppAgentInstallationRoster", gen.AppAgentInstallationRoster_Operation, map[string]any{"appRef": provenanceApp1682}},
	}
	if err := checkProvenanceRequests1682(valid, steps); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]provenanceRequest1682{
		"extra":   append(append([]provenanceRequest1682{}, valid...), valid[1]),
		"missing": valid[:1], "order": {valid[1], valid[0]},
		"variable":  {{valid[0].OperationName, valid[0].Query, map[string]any{"appRef": "wrong"}}, valid[1]},
		"operation": {{"AppAgentInstallationRoster", valid[0].Query, valid[0].Variables}, valid[1]},
		"selection": {{valid[0].OperationName, gen.AppAgentRoster_Operation, valid[0].Variables}, valid[1]},
	}
	for name, got := range cases {
		t.Run(name, func(t *testing.T) {
			if checkProvenanceRequests1682(got, steps) == nil {
				t.Fatal("invalid request trace accepted")
			}
		})
	}
}

func TestAppAgentProvenanceSchemaSkew1682(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy%t/json%t", legacy, asJSON), func(t *testing.T) {
				out, stderr, code, err := runProvenance1682(t, provenanceSteps1682(missingMethod1682, legacy), asJSON)
				if err == nil || code != 2 {
					t.Fatalf("exit=%d err=%v", code, err)
				}
				message := stderr
				if asJSON {
					var envelope map[string]map[string]any
					if json.Unmarshal([]byte(out), &envelope) != nil || len(envelope) != 1 || len(envelope["error"]) != 2 || envelope["error"]["code"] != float64(2) {
						t.Fatalf("not the root error envelope: %s", out)
					}
					message, _ = envelope["error"]["message"].(string)
					if stderr != "" {
						t.Fatalf("JSON stderr: %s", stderr)
					}
				} else if out != "" {
					t.Fatalf("success output on refusal: %s", out)
				}
				want := "the server rejected a query this `hadron` build sends — the CLI and server schema versions are out of sync. " +
					"Update whichever is behind: upgrade the CLI (e.g. `brew upgrade hadron`), or the server if it is self-hosted. " +
					"`hadron version` shows the CLI build. (server said: Cannot query field \"installMethod\" on type \"AppAgent\".)"
				if !asJSON {
					want = "hadron: " + want + "\n"
				}
				if message != want {
					t.Fatalf("mapped refusal=%q want%q", message, want)
				}

			})
		}
	}
}

func provenanceFixture1682(t *testing.T) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal([]byte(appRosterResp), &p); err != nil {
		t.Fatal(err)
	}
	app := p["data"].(map[string]any)["app"].(map[string]any)
	app["appAgents"] = []any{map[string]any{"agent": map[string]any{"id": "agt9"}, "installMethod": "ROW", "importer": nil}}
	return p
}

func TestAppAgentProvenanceNullableAndRobustness1682(t *testing.T) {
	for _, kind := range []string{"legal-null-agent-all-provenance-withheld", "synthetic-null-list-entries", "synthetic-missing-join", "synthetic-future-method"} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/legacy%t", kind, legacy), func(t *testing.T) {
				p := provenanceFixture1682(t)
				app := p["data"].(map[string]any)["app"].(map[string]any)
				rows := app["appAgents"].([]any)
				row := rows[0].(map[string]any)
				var method any = "ROW"
				var importer any
				switch kind {
				case "legal-null-agent-all-provenance-withheld":
					// A legal unreadable join withholds provenance on EVERY entry, not
					// only the hidden entry. Client copies this policy; it never decides it.
					row["installMethod"] = nil
					row["importer"] = nil
					app["appAgents"] = append(rows, map[string]any{"agent": nil, "installMethod": nil, "importer": nil})
					method = nil
				case "synthetic-null-list-entries":
					// Both SDL lists have non-null elements: intentionally malformed data.
					app["agents"] = append(app["agents"].([]any), nil)
					app["appAgents"] = append(rows, nil)
				case "synthetic-missing-join":
					// Readable member without its matching join is defensive synthetic data.
					app["appAgents"] = []any{}
					method = nil
				case "synthetic-future-method":
					// Not in current ROW/DERIVED SDL: hypothetical newer-provider token.
					method = "FUTURE_METHOD"
					importer = map[string]any{"id": "agt9", "urn": "hrn:agent:acme.com:support-bot", "name": "Support Bot"}
					row["installMethod"] = method
					row["importer"] = importer
				}
				wire, err := json.Marshal(p)
				if err != nil {
					t.Fatal(err)
				}
				out, _, code, err := runProvenance1682(t, provenanceSteps1682(string(wire), legacy), true)
				if err != nil || code != 0 {
					t.Fatalf("exit%d %v: %s", code, err, out)
				}
				var got []map[string]any
				if json.Unmarshal([]byte(out), &got) != nil || len(got) != 1 {
					t.Fatalf("readable member lost/extra: %s", out)
				}
				for key, want := range map[string]any{"installMethod": method, "importer": importer} {
					v, exists := got[0][key]
					if !exists || !reflect.DeepEqual(v, want) {
						t.Fatalf("%s=%#v exists%t want%#v", key, v, exists, want)
					}
				}
				human, _, _, err := runProvenance1682(t, provenanceSteps1682(string(wire), legacy), false)
				if err != nil {
					t.Fatal(err)
				}
				cells := []string{"installed", "—"}
				if method == nil {
					cells = []string{"?", "?"}
				}
				if kind == "synthetic-future-method" {
					cells = []string{"FUTURE_METHOD", "Support Bot (hrn:agent:acme.com:support-bot)"}
				}
				if err := checkProvenanceTable1682(human, map[string][]string{"Support Bot": cells}, legacy); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
