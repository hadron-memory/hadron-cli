package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// hadron-cli#1014 (server#1507, provider server#2366): `memory get` shows the
// stored per-file outcome of the last sync from Memory.lastSyncReport. The
// server owns the report; the CLI only renders it, and never turns a missing,
// unreadable or partial report into a clean or a failed claim.

// syncMemoryJSON is memoryJSON plus the sync fields. A nil pointer is JSON null;
// report is the raw lastSyncReport STRING (already JSON text, so it is encoded
// as a string here).
func syncMemoryJSON(t *testing.T, source, lastSyncedAt, syncError, report *string, status string) string {
	t.Helper()
	enc := func(v *string) string {
		if v == nil {
			return "null"
		}
		b, err := json.Marshal(*v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return `{"source":` + enc(source) + `,"lastSyncedAt":` + enc(lastSyncedAt) +
		`,"syncError":` + enc(syncError) + `,"lastSyncReport":` + enc(report) +
		`,"syncStatus":"` + status + `",` + strings.TrimPrefix(memoryJSON, "{")
}

func sp(s string) *string { return &s }

func runMemoryGetSync(t *testing.T, memory string, jsonOut bool) (string, error) {
	t.Helper()
	gql, _ := captureGraphQL(t, map[string]string{
		"GetMemoryDetail": `{"data":{"memory":` + memory + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := []string{"memory", "get", "acme.com::kb", "--server", gql.URL}
	if jsonOut {
		args = append(args, "--json")
	}
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func memoryGetSyncJSON(t *testing.T, memory string) map[string]any {
	t.Helper()
	out, err := runMemoryGetSync(t, memory, true)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	return dto
}

const (
	syncedAt   = "2026-10-20T10:00:00.000Z"
	recordedAt = "2026-10-20T10:00:01.000Z"
	gitSource  = "https://github.com/acme/kb"
)

func partialReport() string {
	return `{"version":1,"outcome":"partial","recordedAt":"` + recordedAt + `","entries":[` +
		`{"kind":"protected","loc":"a:b","code":"PROTECTED_LOC"},` +
		`{"kind":"held_move","loc":"new:place","fromLoc":"old:place","code":"MOVE_HELD"}]}`
}

// Red on main: there is no sync block at all, and the report is not requested.
func TestMemoryGetPartialReportShowsPerFileOutcomesNotFailure(t *testing.T) {
	m := syncMemoryJSON(t, sp(gitSource), sp(syncedAt), nil, sp(partialReport()), "OK")
	out, err := runMemoryGetSync(t, m, false)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{
		"sync: last sync committed with 2 file(s) skipped or held",
		"sync status: OK, last synced " + syncedAt,
		"sync report recorded: " + recordedAt,
		`- protected: "a:b" (PROTECTED_LOC)`,
		`- move held: "new:place" from "old:place" (MOVE_HELD)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "failed") {
		t.Errorf("a committed partial sync must not read as failed:\n%s", out)
	}

	dto := memoryGetSyncJSON(t, m)
	if dto["syncReportStatus"] != "recorded" {
		t.Errorf("syncReportStatus = %v, want recorded", dto["syncReportStatus"])
	}
	if dto["lastSyncedAt"] != syncedAt || dto["syncStatus"] != "OK" {
		t.Errorf("lastSyncedAt/syncStatus = %v/%v", dto["lastSyncedAt"], dto["syncStatus"])
	}
	rep, ok := dto["syncReport"].(map[string]any)
	if !ok {
		t.Fatalf("syncReport must be a parsed object, got %T %v", dto["syncReport"], dto["syncReport"])
	}
	if rep["version"] != float64(1) || rep["outcome"] != "partial" || rep["recordedAt"] != recordedAt {
		t.Errorf("report header = %v", rep)
	}
	entries, _ := rep["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %v", rep["entries"])
	}
	held, _ := entries[1].(map[string]any)
	if held["kind"] != "held_move" || held["loc"] != "new:place" || held["fromLoc"] != "old:place" || held["code"] != "MOVE_HELD" {
		t.Errorf("held entry = %v", held)
	}
	if first, _ := entries[0].(map[string]any); first != nil {
		if _, present := first["fromLoc"]; present {
			t.Errorf("an entry without fromLoc must omit it, got %v", first)
		}
	}
}

func TestMemoryGetFailedReportShowsCodeAndErrorSeparately(t *testing.T) {
	report := `{"version":1,"outcome":"failed","recordedAt":"` + recordedAt + `","entries":[],"code":"SYNC_FAILED"}`
	m := syncMemoryJSON(t, sp(gitSource), sp(syncedAt), sp("Git command failed. Check the repository and branch, then retry."), sp(report), "ERROR")
	out, err := runMemoryGetSync(t, m, false)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{
		"sync: last sync failed (SYNC_FAILED)",
		"sync status: ERROR",
		`sync error: "Git command failed. Check the repository and branch, then retry."`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	dto := memoryGetSyncJSON(t, m)
	rep, _ := dto["syncReport"].(map[string]any)
	if rep["outcome"] != "failed" || rep["code"] != "SYNC_FAILED" {
		t.Errorf("syncReport = %v", dto["syncReport"])
	}
	if dto["syncError"] != "Git command failed. Check the repository and branch, then retry." {
		t.Errorf("syncError = %v", dto["syncError"])
	}
}

// An unreadable report is "report unavailable" — never success, never a guess.
func TestMemoryGetUnreadableReportIsUnavailable(t *testing.T) {
	for name, report := range map[string]string{
		"invalid JSON":        `{not json`,
		"unknown version":     `{"version":2,"outcome":"partial","recordedAt":"` + recordedAt + `","entries":[]}`,
		"missing version":     `{"outcome":"partial","recordedAt":"` + recordedAt + `","entries":[]}`,
		"string payload":      `"partial"`,
		"number payload":      `123`,
		"array payload":       `[]`,
		"null payload":        `null`,
		"unknown outcome":     `{"version":1,"outcome":"clean","recordedAt":"` + recordedAt + `","entries":[]}`,
		"bad recordedAt":      `{"version":1,"outcome":"partial","recordedAt":"yesterday\u001b[2J","entries":[]}`,
		"entry of wrong type": `{"version":1,"outcome":"partial","recordedAt":"` + recordedAt + `","entries":[{"kind":"protected","loc":7,"code":"PROTECTED_LOC"}]}`,
		"entry missing loc":   `{"version":1,"outcome":"partial","recordedAt":"` + recordedAt + `","entries":[{"kind":"protected","code":"PROTECTED_LOC"}]}`,
		// v1 requires the entries array (Diego, cli#1067 BLIND P2): an omitted or
		// null one is not an empty report. An explicit [] stays valid.
		"missing entries":   `{"version":1,"outcome":"partial","recordedAt":"` + recordedAt + `"}`,
		"null entries":      `{"version":1,"outcome":"partial","recordedAt":"` + recordedAt + `","entries":null}`,
		"entries not array": `{"version":1,"outcome":"partial","recordedAt":"` + recordedAt + `","entries":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			m := syncMemoryJSON(t, sp(gitSource), sp(syncedAt), nil, sp(report), "OK")
			out, err := runMemoryGetSync(t, m, false)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !strings.Contains(out, "sync: report unavailable") {
				t.Errorf("want report unavailable:\n%s", out)
			}
			for _, never := range []string{"completed", "no per-file issues", "committed", "\x1b"} {
				if strings.Contains(out, never) {
					t.Errorf("an unreadable report must not print %q:\n%s", never, out)
				}
			}
			dto := memoryGetSyncJSON(t, m)
			if v, present := dto["syncReport"]; !present || v != nil {
				t.Errorf("syncReport = %v (present %v), want null", v, present)
			}
			if dto["syncReportStatus"] != "unavailable" {
				t.Errorf("syncReportStatus = %v, want unavailable", dto["syncReportStatus"])
			}
		})
	}
}

func TestMemoryGetNullReportNeverSyncedMakesNoCleanClaim(t *testing.T) {
	m := syncMemoryJSON(t, sp(gitSource), nil, nil, nil, "PENDING")
	out, err := runMemoryGetSync(t, m, false)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "sync: never synced") || !strings.Contains(out, "last synced never") {
		t.Errorf("want never synced:\n%s", out)
	}
	for _, never := range []string{"completed", "no per-file issues", "clean"} {
		if strings.Contains(out, never) {
			t.Errorf("a never-synced memory must not print %q:\n%s", never, out)
		}
	}
	dto := memoryGetSyncJSON(t, m)
	if v, present := dto["syncReport"]; !present || v != nil {
		t.Errorf("syncReport = %v (present %v), want null", v, present)
	}
	if dto["syncReportStatus"] != "none" {
		t.Errorf("syncReportStatus = %v, want none", dto["syncReportStatus"])
	}
	if v, present := dto["lastSyncedAt"]; !present || v != nil {
		t.Errorf("lastSyncedAt = %v (present %v), want null", v, present)
	}
}

func TestMemoryGetNullReportAfterSync(t *testing.T) {
	out, err := runMemoryGetSync(t, syncMemoryJSON(t, sp(gitSource), sp(syncedAt), nil, nil, "OK"), false)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "sync: last sync completed, no per-file issues recorded") {
		t.Errorf("want completed wording:\n%s", out)
	}
	if strings.Contains(out, "sync report recorded") {
		t.Errorf("no report, so no recorded line:\n%s", out)
	}
}

// A null report beside a non-OK status (a row from before the provider, or a
// sync still running) must not borrow the clean wording.
func TestMemoryGetNullReportDefersToANonOKStatus(t *testing.T) {
	for status, want := range map[string]string{
		"ERROR":   "sync: last sync failed, no per-file report recorded",
		"SYNCING": "sync: sync in progress, no per-file report recorded",
		"PENDING": "sync: sync pending, no per-file report recorded",
	} {
		t.Run(status, func(t *testing.T) {
			out, err := runMemoryGetSync(t, syncMemoryJSON(t, sp(gitSource), sp(syncedAt), sp("boom"), nil, status), false)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !strings.Contains(out, want) {
				t.Errorf("want %q:\n%s", want, out)
			}
			if strings.Contains(out, "completed") {
				t.Errorf("must not read as completed:\n%s", out)
			}
			if !strings.Contains(out, `sync error: "boom"`) {
				t.Errorf("syncError shows whenever present:\n%s", out)
			}
		})
	}
}

func TestMemoryGetUnknownOutcomeNeverReadsClean(t *testing.T) {
	report := `{"version":1,"outcome":"unknown","recordedAt":"` + recordedAt + `","entries":[{"kind":"deferred","loc":"x","code":"ROW_CHANGED"}]}`
	out, err := runMemoryGetSync(t, syncMemoryJSON(t, sp(gitSource), sp(syncedAt), nil, sp(report), "OK"), false)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "sync: last sync completed but some skips could not be listed") {
		t.Errorf("want unknown wording:\n%s", out)
	}
	if !strings.Contains(out, `- deferred: "x" (ROW_CHANGED)`) {
		t.Errorf("retained entries still print:\n%s", out)
	}
	if strings.Contains(out, "no per-file issues") {
		t.Errorf("unknown must not read clean:\n%s", out)
	}
}

// Server-supplied strings reach a terminal: quoted (no raw control or escape
// sequences), unknown vocabulary labelled "other", and URL credentials masked.
func TestMemoryGetSyncStringsAreQuotedAndUnknownVocabularyIsOther(t *testing.T) {
	report := `{"version":1,"outcome":"partial","recordedAt":"` + recordedAt + `","entries":[` +
		`{"kind":"protected","loc":"evil\u001b[31m\nline","code":"PROTECTED_LOC"},` +
		`{"kind":"new_kind\u001b]0;x\u0007","loc":"y","code":"NEW_CODE"}],"code":"NEW_TOP\u001b"}`
	syncErr := "fetch https://x-access-token:ghs_SECRET@github.com/acme/kb.git failed\u001b[2J\nnext"
	m := syncMemoryJSON(t, sp(gitSource), sp(syncedAt), sp(syncErr), sp(report), "OK")
	out, err := runMemoryGetSync(t, m, false)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, never := range []string{"\x1b", "\x07", "ghs_SECRET", "evil\x1b", "\nline", "\nnext"} {
		if strings.Contains(out, never) {
			t.Errorf("output must not contain %q:\n%s", never, out)
		}
	}
	for _, want := range []string{
		`- protected: "evil\x1b[31m\nline" (PROTECTED_LOC)`,
		`- other "new_kind\x1b]0;x\a": "y" (other "NEW_CODE")`,
		`sync error: "fetch https://[redacted]@github.com/acme/kb.git failed\x1b[2J\nnext"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	dto := memoryGetSyncJSON(t, m)
	if s, _ := dto["syncError"].(string); strings.Contains(s, "ghs_SECRET") {
		t.Errorf("--json syncError must mask URL credentials too: %q", s)
	}
	rep, _ := dto["syncReport"].(map[string]any)
	entries, _ := rep["entries"].([]any)
	if e, _ := entries[1].(map[string]any); e == nil || e["kind"] != "new_kind\x1b]0;x\a" || e["code"] != "NEW_CODE" {
		t.Errorf("--json passes unknown vocabulary through as-is: %v", entries)
	}
}

// The two guards Rodolfo's §10 mutants showed unpinned (cli#1067): an unknown
// report-level code in the failed summary is quoted, and syncError is capped.
// Also: a password with an unencoded @ is masked whole, not up to its first @.
func TestMemoryGetFailedSummaryAndSyncErrorAreGuarded(t *testing.T) {
	report := `{"version":1,"outcome":"failed","recordedAt":"` + recordedAt + `","entries":[],"code":"NEW\u001b[31m"}`
	syncErr := "fetch https://user:p@ss@github.com/acme/kb.git failed " + strings.Repeat("e", 600)
	m := syncMemoryJSON(t, sp(gitSource), sp(syncedAt), sp(syncErr), sp(report), "ERROR")
	out, err := runMemoryGetSync(t, m, false)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `sync: last sync failed (other "NEW\x1b[31m")`) || strings.Contains(out, "\x1b") {
		t.Errorf("an unknown report code is quoted under other:\n%s", out)
	}
	if strings.Contains(out, "ss@github.com") || !strings.Contains(out, "https://[redacted]@github.com/acme/kb.git") {
		t.Errorf("the whole userinfo is masked, up to the last @:\n%s", out)
	}
	if strings.Contains(out, strings.Repeat("e", 501)) || !strings.Contains(out, `"…`) {
		t.Errorf("syncError is capped at 500 runes with a … marker:\n%s", out)
	}
	dto := memoryGetSyncJSON(t, m)
	if s, _ := dto["syncError"].(string); strings.Contains(s, "ss@github.com") {
		t.Errorf("--json masks the whole userinfo too: %q", s)
	}
}

func TestMemoryGetSyncEntryListAndLocAreCapped(t *testing.T) {
	var entries []string
	long := strings.Repeat("a", 500)
	entries = append(entries, `{"kind":"protected","loc":"`+long+`","code":"PROTECTED_LOC"}`)
	for i := 1; i < 60; i++ {
		entries = append(entries, fmt.Sprintf(`{"kind":"protected","loc":"n%02d","code":"PROTECTED_LOC"}`, i))
	}
	report := `{"version":1,"outcome":"partial","recordedAt":"` + recordedAt + `","entries":[` + strings.Join(entries, ",") + `]}`
	m := syncMemoryJSON(t, sp(gitSource), sp(syncedAt), nil, sp(report), "OK")
	out, err := runMemoryGetSync(t, m, false)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "60 file(s) skipped or held") {
		t.Errorf("the count is the true total, not the cap:\n%s", out)
	}
	if got := strings.Count(out, "    - protected: "); got != 50 {
		t.Errorf("printed %d entries, want the cap of 50", got)
	}
	if !strings.Contains(out, "    … and 10 more") {
		t.Errorf("want the overflow summary:\n%s", out)
	}
	if strings.Contains(out, long) || !strings.Contains(out, `"`+strings.Repeat("a", 200)+`"…`) {
		t.Errorf("a long loc is capped at 200 runes with a … marker:\n%s", out[:400])
	}
	// --json is the machine contract: every entry, uncapped.
	dto := memoryGetSyncJSON(t, m)
	rep, _ := dto["syncReport"].(map[string]any)
	if es, _ := rep["entries"].([]any); len(es) != 60 {
		t.Errorf("--json entries = %d, want all 60", len(es))
	}
}

// A memory with no git source and no sync history prints no sync block, so the
// existing get output is unchanged for it.
func TestMemoryGetWithoutSyncHistoryPrintsNoSyncBlock(t *testing.T) {
	out, err := runMemoryGetSync(t, syncMemoryJSON(t, nil, nil, nil, nil, "PENDING"), false)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out, "sync") {
		t.Errorf("no sync block expected:\n%s", out)
	}
}

// The report fields ride a memory-get-only operation: the shared GetMemory
// (resolved by a dozen other commands) must keep working against a server
// that predates the field, and memory get must use the detail read.
func TestOnlyMemoryGetRequestsTheSyncReport(t *testing.T) {
	for _, field := range []string{"lastSyncReport", "lastSyncedAt", "syncError"} {
		if strings.Contains(gen.GetMemory_Operation, field) {
			t.Errorf("shared GetMemory must not select %s", field)
		}
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"GetMemoryDetail": `{"data":{"memory":` + memoryJSON + `}}`,
		"GetMemory":       `{"data":{"memory":` + memoryJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "get", "acme.com::kb", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["GetMemoryDetail"]; !ok {
		t.Errorf("memory get must use GetMemoryDetail, sent %v", captured)
	}
	if _, ok := captured["GetMemory"]; ok {
		t.Errorf("memory get must not use the shared GetMemory")
	}
}
