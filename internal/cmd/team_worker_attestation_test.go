package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestWorkerRmExternalAttestation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "yes-is-not-attestation", true: "explicit-attestation"}[explicit], func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"Workers": staffJSON, "WorkersRoster": staffJSON,
				"DeleteWorker": `{"data":{"deleteWorker":true}}`,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"team", "worker", "rm", "Iris", "--yes", "--json", "--app", "acme.com:eng-team", "--server", gql.URL}
			if explicit {
				args = append(args, "--confirm-no-external-attribution")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var vars map[string]any
			if err := json.Unmarshal(captured["DeleteWorker"], &vars); err != nil {
				t.Fatal(err)
			}
			value, present := vars["confirmNoExternalAttribution"]
			if present != explicit || (explicit && value != true) {
				t.Fatalf("attestation = %v (present %v), explicit %v", value, present, explicit)
			}
			var result map[string]any
			if err := json.Unmarshal([]byte(out.String()), &result); err != nil || len(result) != 3 || result["id"] != "wkr1" || result["name"] != "Iris" || result["status"] != "deleted" {
				t.Fatalf("stable JSON: %s (%v)", out, err)
			}
		})
	}
}

func TestWorkerRmMissingAttestationIsUsage(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Workers": staffJSON, "WorkersRoster": staffJSON,
		"DeleteWorker": `{"errors":[{"message":"Verify no external attribution; known or uncertain history means retire instead","extensions":{"code":"WORKER_EXTERNAL_ATTRIBUTION_UNCONFIRMED"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "rm", "Iris", "--yes", "--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "external attribution") {
		t.Fatalf("expected actionable Usage refusal, got %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteWorker"], &vars)
	if _, present := vars["confirmNoExternalAttribution"]; present {
		t.Fatalf("--yes attested: %v", vars)
	}
}

func TestWorkerRmExternalAttestationHelp(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "rm", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"--confirm-no-external-attribution", "Database checks cannot prove", "uncertain", "--yes"} {
		if !strings.Contains(out.String(), phrase) {
			t.Errorf("help lacks %q: %s", phrase, out)
		}
	}
}
