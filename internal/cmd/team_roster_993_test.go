package cmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// cli#993: `team roster` / `team worker list` show the server's self-reported
// MODEL and advisory TIER, and the roster prints a per-tier tally of the rows
// the server marked freeForWork.

// rosterRow993 builds one TeamRoster entry. model/tier are JSON literals
// ("null" or a quoted string) so a fixture can spell an unmapped tier.
func rosterRow993(name, state string, free bool, model, tier string) string {
	return fmt.Sprintf(`{"worker":{"id":"w-%s","kind":"WORKER","name":"%s","urn":null},"state":"%s","role":"engineer",
		"bound":true,"liveSession":true,"family":"claude","model":%s,"modelReportedAt":%s,"tier":%s,
		"freeForWork":%t,"freeFamilies":[],"launchableFamilies":[],"unavailableFamilies":[],
		"unavailableReason":null,"unavailableUntil":null,"lastActiveAt":null,"checkStatus":null,"lastAssignedAt":null,
		"pendingTasks":[],"startedTasks":[],"waitingOn":[],"waitingSince":null,"unreactedChildTask":null,
		"modelGroups":[],"alarms":[],"reportsTo":null,"reportsToRetired":false}`,
		name, name, state, model, map[bool]string{true: `"2026-10-24T00:00:00Z"`, false: "null"}[model != "null"], tier, free)
}

func rosterResponses993(rows ...string) map[string]string {
	r := taskResponses()
	r["TeamRoster"] = `{"data":{"teamRoster":{"appId":"capp100000000000000000000","asOf":"2026-10-24T01:00:00Z","workers":[` + strings.Join(rows, ",") + `]}}}`
	return r
}

var capacityRe993 = regexp.MustCompile(`free for work by tier: high (\d+) · mid (\d+) · low (\d+) · unknown (\d+)`)

func capacity993(t *testing.T, out string) [4]string {
	t.Helper()
	m := capacityRe993.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no capacity line in:\n%s", out)
	}
	return [4]string{m[1], m[2], m[3], m[4]}
}

func TestTeamRosterShowsModelAndTierBesideFamily(t *testing.T) {
	calls, out, err := runTeamTask(t, rosterResponses993(
		rosterRow993("Ann", "AVAILABLE", true, `"claude-opus-5-5"`, `"high"`),
		rosterRow993("Bo", "AVAILABLE", true, "null", "null"),
	), "team", "roster")
	if err != nil {
		t.Fatal(err)
	}
	header := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "WORKER") && strings.Contains(l, "FAMILY") {
			header = l
		}
	}
	fam, mod, tier, live := strings.Index(header, "FAMILY"), strings.Index(header, "MODEL"), strings.Index(header, "TIER"), strings.Index(header, "LIVE")
	if fam < 0 || fam >= mod || mod >= tier || tier >= live {
		t.Errorf("want FAMILY < MODEL < TIER < LIVE, header %q", header)
	}
	if !strings.Contains(out, "claude-opus-5-5") || !strings.Contains(out, "high") {
		t.Errorf("values missing:\n%s", out)
	}
	// One read only: no second roster/workers join (gate: no second read).
	reads := 0
	for _, c := range *calls {
		if strings.HasPrefix(c.Op, "Team") && c.Op != "TeamAppIdentity" || strings.HasPrefix(c.Op, "Workers") {
			reads++
		}
	}
	if reads != 1 {
		t.Errorf("want exactly one roster read, got %d: %v", reads, *calls)
	}
}

// Every key the roster --json carried before is still there; the three new
// ones are additive, and a null stays null while an unknown tier passes through.
func TestTeamRosterJSONAdditiveModelTier(t *testing.T) {
	_, out, err := runTeamTask(t, rosterResponses993(
		rosterRow993("Ann", "AVAILABLE", true, `"m1"`, `"ultra"`),
		rosterRow993("Bo", "AVAILABLE", true, "null", "null"),
	), "team", "roster", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Workers []map[string]json.RawMessage `json:"workers"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"worker", "state", "role", "bound", "liveSession", "family", "freeForWork", "freeFamilies",
		"launchableFamilies", "unavailableFamilies", "unavailableReason", "unavailableUntil", "lastActiveAt", "checkStatus",
		"lastAssignedAt", "pendingTasks", "startedTasks", "waitingOn", "waitingSince", "unreactedChildTask", "alarms",
		"reportsTo", "reportsToRetired", "model", "modelReportedAt", "tier"} {
		for i, w := range dto.Workers {
			if _, ok := w[k]; !ok {
				t.Errorf("worker %d lost/missing key %q", i, k)
			}
		}
	}
	if string(dto.Workers[0]["tier"]) != `"ultra"` || string(dto.Workers[0]["model"]) != `"m1"` {
		t.Errorf("unknown tier must pass through untouched: %v", dto.Workers[0])
	}
	if string(dto.Workers[1]["tier"]) != "null" || string(dto.Workers[1]["model"]) != "null" || string(dto.Workers[1]["modelReportedAt"]) != "null" {
		t.Errorf("nulls must stay null: %v", dto.Workers[1])
	}
	if strings.Contains(out, "free for work by tier") {
		t.Errorf("the tally is table-only; --json must stay the server's rows")
	}
}

// The tally counts freeForWork rows (NOT state == AVAILABLE), by tier, with
// null and unrecognised tiers in "unknown" and never folded into a real bucket.
func TestTeamRosterCapacityTalliesFreeForWorkByTier(t *testing.T) {
	_, out, err := runTeamTask(t, rosterResponses993(
		rosterRow993("a", "AVAILABLE", true, `"m"`, `"high"`),
		rosterRow993("b", "WORKING", true, `"m"`, `"high"`),   // free though not AVAILABLE: counts
		rosterRow993("c", "AVAILABLE", false, `"m"`, `"low"`), // AVAILABLE but NOT free: must not count (its own tier, so it cannot cancel b)
		rosterRow993("d", "NO_FAMILY", false, `"m"`, `"mid"`), // not free
		rosterRow993("e", "AVAILABLE", true, `"m"`, `"mid"`),
		rosterRow993("f", "AVAILABLE", true, `"m"`, `"low"`),
		rosterRow993("g", "AVAILABLE", true, `"m"`, `"ultra"`), // unrecognised
		rosterRow993("h", "AVAILABLE", true, "null", "null"),   // null
		rosterRow993("i", "AVAILABLE", true, `"m"`, `"HIGH"`),  // case differs: unknown, not high
	), "team", "roster")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := capacity993(t, out), [4]string{"2", "1", "1", "3"}; got != want {
		t.Errorf("tally high/mid/low/unknown = %v, want %v\n%s", got, want, out)
	}
	if !strings.Contains(out, "advisory") {
		t.Errorf("tally line must call tier advisory:\n%s", out)
	}
}

func TestTeamRosterCapacityEmptyRosterIsAZeroLine(t *testing.T) {
	_, out, err := runTeamTask(t, rosterResponses993(), "team", "roster")
	if err != nil {
		t.Fatal(err)
	}
	if got := capacity993(t, out); got != [4]string{"0", "0", "0", "0"} {
		t.Errorf("empty roster must tally zeros, got %v", got)
	}
}

func TestTeamRosterHelpNamesModelTierAsAdvisory(t *testing.T) {
	_, out, err := runTeamTask(t, taskResponses(), "team", "roster", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SELF-REPORTED", "never authorization", "freeForWork"} {
		if !strings.Contains(out, want) {
			t.Errorf("roster help missing %q", want)
		}
	}
}

// `team worker list` gains MODEL/TIER (no FAMILY) and its JSON carries the
// three keys with the server's nulls.
func TestTeamWorkerListModelTier(t *testing.T) {
	stubs := activityStubs(t)
	withModel := strings.Replace(workerWith(t, "Iris", "wkr1", "u-holger", agoRFC3339(4*3600e9), "true"),
		`"memoryId":"mw1"`, `"memoryId":"mw1","model":"claude-opus-5-5","modelReportedAt":"2026-10-24T00:00:00Z","tier":"high"`, 1)
	bare := workerWith(t, "Mira", "wkr3", "", "", "false")
	stubs["Workers"] = `{"data":{"workers":{"total":2,"items":[` + withModel + `,` + bare + `]}}}`
	stubs["WorkersRoster"] = stubs["Workers"]
	teamGitDir(t)
	gql, _ := captureGraphQL(t, stubs)
	run := func(extra ...string) string {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--server", gql.URL}, extra...))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	table := run()
	if !strings.Contains(table, "MODEL") || !strings.Contains(table, "TIER") {
		t.Errorf("worker list wants MODEL and TIER:\n%s", table)
	}
	if !strings.Contains(table, "claude-opus-5-5") || !strings.Contains(table, "high") {
		t.Errorf("values missing:\n%s", table)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(run("--json")), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("json: %v", err)
	}
	if string(rows[0]["tier"]) != `"high"` || string(rows[1]["tier"]) != "null" || string(rows[1]["model"]) != "null" {
		t.Errorf("tier/model must pass through with nulls: %v %v", rows[0]["tier"], rows[1]["tier"])
	}
	if _, ok := rows[1]["modelReportedAt"]; !ok {
		t.Errorf("modelReportedAt must always be present")
	}
}
