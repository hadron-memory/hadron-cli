package cmd

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// validateResp builds a ValidateMemory response body. findings/skipped are raw
// JSON fragments so each test can shape exactly the result it needs.
func validateResp(total int, truncated bool, ok bool, findings, skipped string) string {
	return `{"data":{"validateMemory":{
		"memoryId":"mem1","nodesChecked":42,"ok":` + boolJSON(ok) + `,
		"totalFindings":` + strconv.Itoa(total) + `,"truncated":` + boolJSON(truncated) + `,
		"findings":[` + findings + `],"skippedChecks":[` + skipped + `]}}}`
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// decodeVars unmarshals the variables captureGraphQL recorded for an operation.
func decodeVars(t *testing.T, reqs map[string]json.RawMessage, op string) map[string]any {
	t.Helper()
	raw, ok := reqs[op]
	if !ok {
		t.Fatalf("no %s request captured", op)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s vars: %v", op, err)
	}
	return v
}

const (
	findBroken = `{"kind":"BROKEN_REF","nodeId":"n1","nodeLoc":"a:b","nodeUrn":"hrn:node:o:m:a:b","detail":"Edge dangling"}`
	findStale  = `{"kind":"STALE_ABSTRACT","nodeId":"n2","nodeLoc":"c:d","nodeUrn":null,"detail":"Abstract may not reflect current content"}`
	findEmbed  = `{"kind":"EMBED_FAILED","nodeId":"n3","nodeLoc":"e:f","nodeUrn":"hrn:node:o:m:e:f","detail":"EmbeddingRequestError: timed out"}`
)

func runValidate(t *testing.T, resp string, extra ...string) (map[string]any, string, error) {
	t.Helper()
	gql := fakeGraphQL(t, map[string]string{
		"GetMemory":      `{"data":{"memory":{"id":"mem1","urn":"hrn:mem:o:m","name":"M","shortDescription":null,"class":"shared","visibility":"private","organizationId":"org1","isEncrypted":false,"maxRevCount":null,"updatedAt":"2026-08-05T00:00:00Z"}}}`,
		"ValidateMemory": resp,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := append([]string{"memory", "validate", "o::m", "--server", gql.URL, "--json"}, extra...)
	root.SetArgs(args)
	err := root.Execute()
	var dto map[string]any
	if jerr := json.Unmarshal([]byte(out.String()), &dto); jerr != nil && err == nil {
		t.Fatalf("output is not JSON: %v\n%s", jerr, out.String())
	}
	return dto, out.String(), err
}

func TestMemoryValidateReportsTotalNotListedCount(t *testing.T) {
	// The load-bearing distinction: `findings` is capped by the server, so a
	// caller gating on its length would read a truncated audit as a smaller
	// problem. totalFindings must survive intact alongside the listed count.
	dto, _, err := runValidate(t, validateResp(245, true, false, findBroken+","+findStale, ""))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := dto["totalFindings"].(float64); got != 245 {
		t.Errorf("totalFindings = %v, want 245", got)
	}
	if got := dto["matchedFindings"].(float64); got != 2 {
		t.Errorf("matchedFindings = %v, want 2 (the listed findings)", got)
	}
	if dto["truncated"] != true {
		t.Error("truncated must be surfaced")
	}
}

func TestMemoryValidateOkFalseOnSkippedCheckWithNoFindings(t *testing.T) {
	// Zero findings is NOT health when a check didn't run. The skipped check
	// and its reason must both reach the caller.
	skipped := `{"check":"STALE_ABSTRACT","reason":"encrypted memory — the validator does not decrypt"}`
	dto, _, err := runValidate(t, validateResp(0, false, false, "", skipped))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if dto["ok"] != false {
		t.Error("ok must stay false when a check was skipped, even with zero findings")
	}
	sk := dto["skippedChecks"].([]any)
	if len(sk) != 1 {
		t.Fatalf("skippedChecks = %v, want 1", sk)
	}
	e := sk[0].(map[string]any)
	if e["check"] != "stale-abstract" {
		t.Errorf("skipped check = %v, want kebab-case stale-abstract", e["check"])
	}
	if !strings.Contains(e["reason"].(string), "decrypt") {
		t.Errorf("skip reason must be carried through; got %v", e["reason"])
	}
}

func TestMemoryValidateHumanOutputWarnsCheckDidNotRun(t *testing.T) {
	// A skipped check is the reason an empty report isn't a clean bill of
	// health, so the human output must say so before anything else.
	gql := fakeGraphQL(t, map[string]string{
		"GetMemory":      `{"data":{"memory":{"id":"mem1","urn":"hrn:mem:o:m","name":"M","shortDescription":null,"class":"shared","visibility":"private","organizationId":"org1","isEncrypted":true,"maxRevCount":null,"updatedAt":"2026-08-05T00:00:00Z"}}}`,
		"ValidateMemory": validateResp(0, false, false, "", `{"check":"STALE_ABSTRACT","reason":"encrypted memory"}`),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "validate", "o::m", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "checks NOT run") {
		t.Errorf("human output must flag un-run checks; got:\n%s", s)
	}
	if strings.Contains(s, "✓ no findings") {
		t.Errorf("must not claim a clean result when a check was skipped; got:\n%s", s)
	}
}

func TestMemoryValidateCleanResultOnlyWhenEveryCheckRan(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetMemory":      `{"data":{"memory":{"id":"mem1","urn":"hrn:mem:o:m","name":"M","shortDescription":null,"class":"shared","visibility":"private","organizationId":"org1","isEncrypted":false,"maxRevCount":null,"updatedAt":"2026-08-05T00:00:00Z"}}}`,
		"ValidateMemory": validateResp(0, false, true, "", ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "validate", "o::m", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "no findings; every check ran") {
		t.Errorf("a genuinely clean audit should say so; got:\n%s", out.String())
	}
}

func TestMemoryValidateCheckFilterNarrowsListingNotTotal(t *testing.T) {
	dto, _, err := runValidate(t, validateResp(3, false, false, findBroken+","+findStale+","+findEmbed, ""), "--check", "broken-ref")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := dto["totalFindings"].(float64); got != 3 {
		t.Errorf("--check must not rewrite totalFindings; got %v, want 3", got)
	}
	if got := dto["matchedFindings"].(float64); got != 1 {
		t.Errorf("matchedFindings = %v, want 1", got)
	}
	fs := dto["findings"].([]any)
	if len(fs) != 1 || fs[0].(map[string]any)["kind"] != "broken-ref" {
		t.Errorf("filter should keep only broken-ref; got %v", fs)
	}
}

func TestMemoryValidateCheckRequestsServerMaxLimit(t *testing.T) {
	// The server caps `findings` BEFORE the client-side --check filter runs,
	// so filtering a default-capped result silently omits matches past the
	// cap. Filtering must therefore ask for the server maximum.
	gql, reqs := captureGraphQL(t, map[string]string{
		"GetMemory":      `{"data":{"memory":{"id":"mem1","urn":"hrn:mem:o:m","name":"M","shortDescription":null,"class":"shared","visibility":"private","organizationId":"org1","isEncrypted":false,"maxRevCount":null,"updatedAt":"2026-08-05T00:00:00Z"}}}`,
		"ValidateMemory": validateResp(1, false, false, findBroken, ""),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "validate", "o::m", "--check", "broken-ref", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := decodeVars(t, reqs, "ValidateMemory")
	if got, ok := vars["limit"].(float64); !ok || int(got) != 1000 {
		t.Errorf("--check should request the server max limit (1000); got %v", vars["limit"])
	}
}

func TestMemoryValidateExplicitLimitWins(t *testing.T) {
	gql, reqs := captureGraphQL(t, map[string]string{
		"GetMemory":      `{"data":{"memory":{"id":"mem1","urn":"hrn:mem:o:m","name":"M","shortDescription":null,"class":"shared","visibility":"private","organizationId":"org1","isEncrypted":false,"maxRevCount":null,"updatedAt":"2026-08-05T00:00:00Z"}}}`,
		"ValidateMemory": validateResp(1, false, false, findBroken, ""),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "validate", "o::m", "--check", "broken-ref", "--limit", "5", "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := decodeVars(t, reqs, "ValidateMemory")["limit"].(float64); int(got) != 5 {
		t.Errorf("an explicit --limit must win over the --check default; got %v", got)
	}
}

func TestMemoryValidateWarnsWhenFilteringATruncatedResult(t *testing.T) {
	// Even at the server max the result can be truncated; a filtered view of
	// it may be missing matches, and a short list must not read as complete.
	gql := fakeGraphQL(t, map[string]string{
		"GetMemory":      `{"data":{"memory":{"id":"mem1","urn":"hrn:mem:o:m","name":"M","shortDescription":null,"class":"shared","visibility":"private","organizationId":"org1","isEncrypted":false,"maxRevCount":null,"updatedAt":"2026-08-05T00:00:00Z"}}}`,
		"ValidateMemory": validateResp(2000, true, false, findStale, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "validate", "o::m", "--check", "broken-ref", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "caps findings before --check") {
		t.Errorf("must warn that a filtered truncated view can miss matches; got:\n%s", s)
	}
}

func TestMemoryValidateStaleAbstractDescribedAsHashComparison(t *testing.T) {
	// #352: the check is abstractOriginHash vs the content hash, so it fires
	// on any body edit. Describing it as "the abstract is wrong" sends people
	// rewriting abstracts that are fine.
	gql := fakeGraphQL(t, map[string]string{
		"GetMemory":      `{"data":{"memory":{"id":"mem1","urn":"hrn:mem:o:m","name":"M","shortDescription":null,"class":"shared","visibility":"private","organizationId":"org1","isEncrypted":false,"maxRevCount":null,"updatedAt":"2026-08-05T00:00:00Z"}}}`,
		"ValidateMemory": validateResp(1, false, false, findStale, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "validate", "o::m", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "body differs from the version the abstract was written against") {
		t.Errorf("stale-abstract gloss must describe what the check measures; got:\n%s", s)
	}
	if strings.Contains(s, "no longer reflects") {
		t.Errorf("must not overstate the check as proof the abstract is wrong; got:\n%s", s)
	}
}

func TestMemoryValidateFailOnFindingsGate(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		findings string
		total    int
		wantCode int
	}{
		{"default exits 0 despite findings", nil, findStale, 176, 0},
		{"gate trips on a matching finding", []string{"--fail-on-findings"}, findStale, 176, exitcode.Conflict},
		{"gate ignores filtered-out findings", []string{"--fail-on-findings", "--check", "broken-ref"}, findStale, 176, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runValidate(t, validateResp(tc.total, false, false, tc.findings, ""), tc.args...)
			code := 0
			if err != nil {
				code = exitCodeFor(err)
			}
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d (err=%v)", code, tc.wantCode, err)
			}
		})
	}
}

func TestMemoryValidateRejectsUnknownCheck(t *testing.T) {
	_, _, err := runValidate(t, validateResp(0, false, true, "", ""), "--check", "nonsense")
	if err == nil {
		t.Fatal("an unknown --check must be a usage error")
	}
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d", got, exitcode.Usage)
	}
	if !strings.Contains(err.Error(), "stale-abstract") {
		t.Errorf("the error should list the valid kinds; got %v", err)
	}
}

func TestMemoryValidateAcceptsWireSpellingForCheck(t *testing.T) {
	// A value copied out of --json or the GraphQL schema should work as-is.
	dto, _, err := runValidate(t, validateResp(2, false, false, findBroken+","+findStale, ""), "--check", "STALE_ABSTRACT")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := dto["matchedFindings"].(float64); got != 1 {
		t.Errorf("SCREAMING_CASE --check should match; matchedFindings = %v", got)
	}
}

func TestMemoryValidateEmptySlicesRenderAsArrays(t *testing.T) {
	// The --json contract: empty collections are [], never null.
	_, raw, err := runValidate(t, validateResp(0, false, true, "", ""))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, field := range []string{`"findings": []`, `"skippedChecks": []`, `"filtered": []`} {
		if !strings.Contains(raw, field) {
			t.Errorf("expected %s in output:\n%s", field, raw)
		}
	}
}

func TestMemoryValidatePreservesNullNodeUrn(t *testing.T) {
	// The server returns nodeUrn: null when a URN can't be composed for the
	// node (compound app-memory shapes, unexpected locs). Flattening that to ""
	// tells a consumer the URN is empty rather than absent, hiding the signal
	// to fall back to nodeId/nodeLoc.
	dto, raw, err := runValidate(t, validateResp(2, false, false, findStale+","+findBroken, ""))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(raw, `"nodeUrn": null`) {
		t.Errorf("a null nodeUrn must stay null in --json, not become \"\":\n%s", raw)
	}
	fs := dto["findings"].([]any)
	if got := fs[0].(map[string]any)["nodeUrn"]; got != nil {
		t.Errorf("findStale has a null nodeUrn; got %#v", got)
	}
	if got := fs[1].(map[string]any)["nodeUrn"]; got != "hrn:node:o:m:a:b" {
		t.Errorf("a present nodeUrn must pass through; got %#v", got)
	}
}

func TestMemoryValidateSummaryIncludesServerAddedKind(t *testing.T) {
	// kindName renders a kind this build doesn't know about, so the summary
	// must list it too — otherwise a new server-side check shows up in the
	// findings table with nothing above it explaining the count, or produces
	// an empty summary entirely.
	const future = `{"kind":"FUTURE_CHECK","nodeId":"n9","nodeLoc":"x:y","nodeUrn":null,"detail":"something new"}`
	gql := fakeGraphQL(t, map[string]string{
		"GetMemory":      `{"data":{"memory":{"id":"mem1","urn":"hrn:mem:o:m","name":"M","shortDescription":null,"class":"shared","visibility":"private","organizationId":"org1","isEncrypted":false,"maxRevCount":null,"updatedAt":"2026-08-05T00:00:00Z"}}}`,
		"ValidateMemory": validateResp(1, false, false, future, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "validate", "o::m", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "future-check") {
		t.Errorf("an unrecognized kind must appear in the summary; got:\n%s", s)
	}
	if !strings.Contains(s, "unknown to this CLI version") {
		t.Errorf("an unrecognized kind needs a fallback gloss, not a blank cell; got:\n%s", s)
	}
}

func TestMemoryValidateUnknownKindCountedInJSON(t *testing.T) {
	const future = `{"kind":"FUTURE_CHECK","nodeId":"n9","nodeLoc":"x:y","nodeUrn":null,"detail":"something new"}`
	dto, _, err := runValidate(t, validateResp(2, false, false, future+","+findStale, ""))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	counts := dto["counts"].(map[string]any)
	if counts["future-check"] != float64(1) {
		t.Errorf("unknown kind should be counted under its kebab-case name; got %v", counts)
	}
	if got := dto["matchedFindings"].(float64); got != 2 {
		t.Errorf("unknown kinds must not be dropped from the listing; matchedFindings = %v", got)
	}
}
