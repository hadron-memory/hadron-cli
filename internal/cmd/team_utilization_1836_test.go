package cmd

// hadron-cli#1836 slice 1 over hadron-server#3828: `team utilization`, a
// read-only view of teamUtilization (family/tier/role, no canAddWorker or
// lastMatchFailure: those fields are not in the schema yet).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const utilizationJSON = `{"data":{"teamUtilization":{"appId":"capp100000000000000000000","windowHours":24,"computedAt":"2026-11-17T17:00:00Z","combinations":[
 {"family":"claude","tier":"high","role":"cli-engineer","eligibleWorkers":4,"busyWorkers":1,"utilization":0.25,"saturated":false},
 {"family":"codex","tier":"mid","role":null,"eligibleWorkers":2,"busyWorkers":2,"utilization":1,"saturated":false},
 {"family":"glm","tier":null,"role":"qa","eligibleWorkers":0,"busyWorkers":0,"utilization":1,"saturated":true}]}}}`

func utilResponses(body string) map[string]string {
	r := taskResponses()
	r["TeamUtilization"] = body
	return r
}

func utilVars(t *testing.T, calls *[]attnCall) map[string]any {
	t.Helper()
	for _, c := range *calls {
		if c.Op == "TeamUtilization" {
			return c.Vars
		}
	}
	t.Fatal("no TeamUtilization call")
	return nil
}

func TestTeamUtilizationAllRanksByUtilizationDescending(t *testing.T) {
	_, out, err := runTeamTask(t, utilResponses(utilizationJSON), "team", "utilization", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		WindowHours  int    `json:"windowHours"`
		ComputedAt   string `json:"computedAt"`
		Combinations []struct {
			Family    string  `json:"family"`
			Tier      *string `json:"tier"`
			Util      float64 `json:"utilization"`
			Saturated bool    `json:"saturated"`
		} `json:"combinations"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if dto.WindowHours != 24 || dto.ComputedAt == "" || len(dto.Combinations) != 3 {
		t.Fatalf("dto: %+v", dto)
	}
	// 1 (codex), 1 (glm; server order kept on a tie), then 0.25.
	got := []string{dto.Combinations[0].Family, dto.Combinations[1].Family, dto.Combinations[2].Family}
	if strings.Join(got, ",") != "codex,glm,claude" {
		t.Fatalf("order %v", got)
	}
	if dto.Combinations[1].Tier != nil {
		t.Fatalf("unknown tier must stay null: %+v", dto.Combinations[1])
	}
}

func TestTeamUtilizationEmptyResultIsAnEmptyList(t *testing.T) {
	empty := `{"data":{"teamUtilization":{"appId":"a","windowHours":24,"computedAt":"2026-11-17T17:00:00Z","combinations":[]}}}`
	_, out, err := runTeamTask(t, utilResponses(empty), "team", "utilization", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"combinations": []`) && !strings.Contains(out, `"combinations":[]`) {
		t.Fatalf("empty must render [], got %s", out)
	}
}

func TestTeamUtilizationSingleCombinationAndFilters(t *testing.T) {
	one := `{"data":{"teamUtilization":{"appId":"a","windowHours":48,"computedAt":"2026-11-17T17:00:00Z","combinations":[
	 {"family":"claude","tier":"high","role":"qa","eligibleWorkers":4,"busyWorkers":1,"utilization":0.25,"saturated":false}]}}}`
	calls, out, err := runTeamTask(t, utilResponses(one), "team", "utilization", "--family", "claude", "--tier", "HIGH", "--role", "qa", "--window", "48h")
	if err != nil {
		t.Fatal(err)
	}
	v := utilVars(t, calls)
	if v["family"] != "claude" || v["tier"] != "high" || v["role"] != "qa" || v["windowHours"] != float64(48) {
		t.Fatalf("vars: %v", v)
	}
	for _, want := range []string{"claude", "high", "qa", "25%", "window: 48h"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestTeamUtilizationWindowUpperBoundIsInclusive(t *testing.T) {
	calls, _, err := runTeamTask(t, utilResponses(utilizationJSON), "team", "utilization", "--all", "--window", "168h")
	if err != nil {
		t.Fatalf("168h is the documented maximum: %v", err)
	}
	if utilVars(t, calls)["windowHours"] != float64(168) {
		t.Fatalf("vars: %v", utilVars(t, calls))
	}
}

func TestTeamUtilizationNullResultIsAnErrorNotAPanicOrEmptyList(t *testing.T) {
	for _, body := range []string{`{"data":{"teamUtilization":null}}`, `{"data":{}}`} {
		_, out, err := runTeamTask(t, utilResponses(body), "team", "utilization", "--all", "--json")
		if err == nil || !strings.Contains(err.Error(), "no utilization result") {
			t.Fatalf("%s: a null/absent result must be a clear error, got err=%v out=%q", body, err, out)
		}
		if strings.TrimSpace(out) != "" {
			t.Fatalf("%s: no success output expected, got %q", body, out)
		}
	}
}

func TestTeamUtilizationJSONKeepsNullTierAndRoleKeys(t *testing.T) {
	_, out, err := runTeamTask(t, utilResponses(utilizationJSON), "team", "utilization", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Combinations []map[string]json.RawMessage `json:"combinations"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	var sawNullTier, sawNullRole bool
	for _, c := range dto.Combinations {
		for _, k := range []string{"tier", "role"} {
			raw, ok := c[k]
			if !ok {
				t.Fatalf("key %q must be present in %v", k, c)
			}
			if string(raw) == "null" {
				sawNullTier = sawNullTier || k == "tier"
				sawNullRole = sawNullRole || k == "role"
			}
		}
	}
	if !sawNullTier || !sawNullRole {
		t.Fatalf("explicit null tier and role rows expected: tier=%v role=%v", sawNullTier, sawNullRole)
	}
}

func TestTeamUtilizationFilteredWithNoMatchIsAnEmptyListExit0(t *testing.T) {
	empty := `{"data":{"teamUtilization":{"appId":"a","windowHours":24,"computedAt":"2026-11-17T17:00:00Z","combinations":[]}}}`
	_, out, err := runTeamTask(t, utilResponses(empty), "team", "utilization", "--family", "claude", "--tier", "low", "--json")
	if err != nil {
		t.Fatalf("no match is an answer, not an error: %v", err)
	}
	if !strings.Contains(out, `"combinations": []`) && !strings.Contains(out, `"combinations":[]`) {
		t.Fatalf("want an empty list, got %s", out)
	}
}

func TestTeamUtilizationOmitsWindowWhenNotGiven(t *testing.T) {
	calls, _, err := runTeamTask(t, utilResponses(utilizationJSON), "team", "utilization", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := utilVars(t, calls)["windowHours"]; ok {
		t.Fatalf("windowHours must be omitted so the server default applies: %v", utilVars(t, calls))
	}
}

func TestTeamUtilizationWithoutAllNeedsOneCombination(t *testing.T) {
	_, _, err := runTeamTask(t, utilResponses(utilizationJSON), "team", "utilization", "--family", "claude")
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("several rows without --all must be a usage error naming --all: %v", err)
	}
}

func TestTeamUtilizationFailOnSaturatedExits5AfterOutput(t *testing.T) {
	_, out, err := runTeamTask(t, utilResponses(utilizationJSON), "team", "utilization", "--all", "--fail-on-saturated", "--json")
	if exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("want exit 5, got %v", err)
	}
	if !strings.Contains(out, `"saturated": true`) && !strings.Contains(out, `"saturated":true`) {
		t.Fatalf("output must be written before the exit: %q", out)
	}
	_, _, err = runTeamTask(t, utilResponses(utilizationJSON), "team", "utilization", "--all")
	if err != nil {
		t.Fatalf("without the flag exit 0: %v", err)
	}
	calm := strings.Replace(utilizationJSON, `"saturated":true`, `"saturated":false`, 1)
	if _, _, err := runTeamTask(t, utilResponses(calm), "team", "utilization", "--all", "--fail-on-saturated"); err != nil {
		t.Fatalf("no saturated row: exit 0, got %v", err)
	}
}

func TestTeamUtilizationBadInputExits2(t *testing.T) {
	for _, argv := range [][]string{
		{"--all", "--tier", "platinum"},
		{"--all", "--window", "soon"},
		{"--all", "--window", "90m"},
		{"--all", "--window", "0h"},
		{"--all", "--window", "169h"},
		{"--all", "--family", "claude"},
		{"--all", "--tier", "high"},
		{"--all", "--role", "qa"},
		{},
	} {
		calls, _, err := runTeamTask(t, utilResponses(utilizationJSON), append([]string{"team", "utilization"}, argv...)...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v: want exit 2, got %v", argv, err)
		}
		for _, c := range *calls {
			if c.Op == "TeamUtilization" {
				t.Errorf("%v: must refuse before calling the server", argv)
			}
		}
	}
}

func TestTeamUtilizationHostileCellsAreSanitized(t *testing.T) {
	evil := `{"data":{"teamUtilization":{"appId":"a","windowHours":24,"computedAt":"2026-11-17T17:00:00\u001b[31mZ","combinations":[
	 {"family":"cl\u001b[31maude","tier":null,"role":"q\u0007a","eligibleWorkers":1,"busyWorkers":0,"utilization":0,"saturated":false}]}}}`
	_, out, err := runTeamTask(t, utilResponses(evil), "team", "utilization", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("control bytes leaked: %q", out)
	}
}

func TestTeamUtilizationOldServerIsASkewRefusal(t *testing.T) {
	old := `{"errors":[{"message":"Cannot query field \"teamUtilization\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	_, _, err := runTeamTask(t, utilResponses(old), "team", "utilization", "--all")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "server") {
		t.Fatalf("want a server-skew refusal, got %v", err)
	}
}

func TestTeamUtilizationRefusalMapsThroughMapError(t *testing.T) {
	deny := `{"errors":[{"message":"no","extensions":{"code":"FORBIDDEN"}}]}`
	_, _, err := runTeamTask(t, utilResponses(deny), "team", "utilization", "--all")
	if exitCodeFor(err) != exitcode.Forbidden {
		t.Fatalf("want exit 8, got %v (%d)", err, exitCodeFor(err))
	}
}
