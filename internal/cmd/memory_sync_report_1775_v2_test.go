package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// hadron-cli#1775 v2 acceptance half (reviewed version 2, hadron-server#3612,
// landed as server PR #3632): version 2 adds the `attestation_refused` entry
// kind with four codes, closed per version — v1 keeps refusing it (pinned in
// TestMemoryGetUnknownVocabulary1775). The DTO carries the wire version
// instead of a hard-coded 1. Unknown vocabulary stays unavailable per the
// #1809 half, and pull keeps its outcome-only exits.

// v2Partial builds a version-2 partial report around the given entries.
func v2Partial(entries string) string {
	return `{"version":2,"outcome":"partial","recordedAt":"` + recordedAt + `","entries":[` + entries + `]}`
}

// A version 2 report is accepted for each of the four attestation codes: a v1
// entry and an attestation entry both print, and --json carries version 2.
func TestMemoryGetSyncReportV2Accepted1775(t *testing.T) {
	for _, code := range []string{
		"AUTHORING_ATTESTATION_MISSING",
		"AUTHORING_ATTESTATION_WRONG_TASK",
		"AUTHORING_ATTESTATION_STALE",
		"AUTHOR_TASK_BROKEN",
	} {
		t.Run(code, func(t *testing.T) {
			report := v2Partial(`{"kind":"protected","loc":"a:b","code":"PROTECTED_LOC"},` +
				`{"kind":"attestation_refused","loc":"c:d","code":"` + code + `"}`)
			m := syncMemoryJSON(t, sp(gitSource), sp(syncedAt), nil, sp(report), "OK")
			out, err := runMemoryGetSync(t, m, false)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			for _, want := range []string{
				"sync: last sync committed with 2 file(s) skipped or held",
				`- protected: "a:b" (PROTECTED_LOC)`,
				`- attestation refused: "c:d" (` + code + `)`,
			} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q:\n%s", want, out)
				}
			}
			dto := memoryGetSyncJSON(t, m)
			if dto["syncReportStatus"] != "recorded" {
				t.Fatalf("syncReportStatus = %v, want recorded", dto["syncReportStatus"])
			}
			rep, _ := dto["syncReport"].(map[string]any)
			if rep["version"] != float64(2) {
				t.Errorf("version = %v, want 2 (the wire version, not a hard-coded 1)", rep["version"])
			}
			entries, _ := rep["entries"].([]any)
			if len(entries) != 2 {
				t.Fatalf("entries = %v", rep["entries"])
			}
			att, _ := entries[1].(map[string]any)
			if att["kind"] != "attestation_refused" || att["loc"] != "c:d" || att["code"] != code {
				t.Errorf("attestation entry = %v", att)
			}
		})
	}
}

// Version 2's vocabulary is closed: an unknown kind, an unknown code, or a
// code that belongs to another kind (either direction) makes the whole report
// unavailable and nothing from it prints.
func TestMemoryGetSyncReportV2UnknownVocabulary1775(t *testing.T) {
	for name, entry := range map[string]string{
		"kind":                `{"kind":"FUTURE_KIND","loc":"DO_NOT_PRINT","code":"PROTECTED_LOC"}`,
		"code":                `{"kind":"attestation_refused","loc":"DO_NOT_PRINT","code":"FUTURE_CODE"}`,
		"att-code-on-v1-kind": `{"kind":"protected","loc":"DO_NOT_PRINT","code":"AUTHORING_ATTESTATION_MISSING"}`,
		"att-code-wrong-kind": `{"kind":"deferred","loc":"DO_NOT_PRINT","code":"AUTHORING_ATTESTATION_STALE"}`,
	} {
		t.Run(name, func(t *testing.T) {
			report := v2Partial(`{"kind":"protected","loc":"PREFIX_MUST_NOT_PRINT","code":"PROTECTED_LOC"},` + entry)
			m := syncMemoryJSON(t, sp(gitSource), sp(syncedAt), nil, sp(report), "OK")
			out, err := runMemoryGetSync(t, m, false)
			if err != nil || !strings.Contains(out, "sync: report unavailable") || strings.Contains(out, "DO_NOT_PRINT") || strings.Contains(out, "PREFIX_MUST_NOT_PRINT") || strings.Contains(out, "other") {
				t.Fatalf("out=%q err=%v", out, err)
			}
			dto := memoryGetSyncJSON(t, m)
			if dto["syncReport"] != nil || dto["syncReportStatus"] != "unavailable" {
				t.Fatal(dto)
			}
		})
	}
}

// The v2 report rides the same pull exits as v1 (#1809's outcome-only read):
// a report that says failed keeps exit 1 even when its vocabulary makes it
// unavailable, and a partial v2 report is a recorded, non-clean success.
func TestMemorySyncPullV2Outcomes1775(t *testing.T) {
	for _, tc := range []struct {
		name, report string
		want         int
		status       string
	}{
		{"failed", `{"version":2,"outcome":"failed","recordedAt":"` + recordedAt + `","entries":[],"code":"SYNC_FAILED"}`, 1, "recorded"},
		{"failed-unknown-code", `{"version":2,"outcome":"failed","recordedAt":"` + recordedAt + `","entries":[],"code":"FUTURE_CODE"}`, 1, "unavailable"},
		{"partial", v2Partial(`{"kind":"attestation_refused","loc":"c:d","code":"AUTHOR_TASK_BROKEN"}`), 0, "recorded"},
		{"partial-wrong-pair", v2Partial(`{"kind":"protected","loc":"x","code":"AUTHORING_ATTESTATION_MISSING"}`), 0, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureSyncPull60(t, map[string]string{"SyncMemory": `{"data":{"syncMemory":` + syncMemoryJSON(t, sp(gitSource), sp(syncedAt), nil, sp(tc.report), "OK") + `}}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "sync", "pull", "acme.com:kb", "--yes", "--json", "--server", gql.URL})
			err := root.Execute()
			if exitCodeFor(err) != tc.want {
				t.Fatal(err, out)
			}
			var dto map[string]any
			if e := json.Unmarshal([]byte(out.String()), &dto); e != nil {
				t.Fatal(e)
			}
			if dto["clean"] != false || dto["syncReportStatus"] != tc.status {
				t.Fatal(dto)
			}
		})
	}
}
