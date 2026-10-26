package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#1085: `team composition` renders the server's teamComposition (#2061 over
// server#2475) as returned — lane order, null buckets, the retired-coordinator
// flag and URNs — and a refused read is a failure, never an empty team.

const composition1085 = `{"data":{"teamComposition":{"appId":"capp100000000000000000000","asOf":"2026-10-26T06:00:00Z","lanes":[
 {"coordinator":{"id":"w-eden","kind":"WORKER","name":"Eden","urn":"hrn:worker:acme.com:eng-team:eden"},"coordinatorRetired":false,
  "countsByRole":[{"role":"engineer","count":2},{"role":null,"count":1}],
  "countsByRoleFamily":[{"role":"engineer","family":"claude","count":1},{"role":"engineer","family":null,"count":1},{"role":null,"family":null,"count":1}],
  "workers":[
   {"worker":{"id":"w-jack","kind":"WORKER","name":"Jack","urn":"hrn:worker:acme.com:eng-team:jack"},"role":"engineer","family":"claude","bound":true,"liveSession":true,"state":"WORKING"},
   {"worker":{"id":"w-ann","kind":"WORKER","name":"Ann","urn":null},"role":"engineer","family":null,"bound":false,"liveSession":false,"state":"NO_FAMILY"},
   {"worker":{"id":"w-nil","kind":"WORKER","name":null,"urn":null},"role":null,"family":null,"bound":false,"liveSession":false,"state":"AVAILABLE"}]},
 {"coordinator":{"id":"w-gone","kind":"WORKER","name":"Gone","urn":"hrn:worker:acme.com:eng-team:gone"},"coordinatorRetired":true,
  "countsByRole":[],"countsByRoleFamily":[],"workers":[]},
 {"coordinator":null,"coordinatorRetired":false,
  "countsByRole":[{"role":"qa","count":1}],"countsByRoleFamily":[{"role":"qa","family":"glm","count":1}],
  "workers":[{"worker":{"id":"w-cedric","kind":"WORKER","name":"Cedric","urn":"hrn:worker:acme.com:eng-team:cedric"},"role":"qa","family":"glm","bound":true,"liveSession":false,"state":"AVAILABLE"}]}
]}}}`

func compositionResponses1085(body string) map[string]string {
	r := taskResponses()
	r["TeamComposition"] = body
	return r
}

func TestTeamCompositionJSONKeepsServerShape(t *testing.T) {
	_, out, err := runTeamTask(t, compositionResponses1085(composition1085), "team", "composition", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		AppID string `json:"appId"`
		AsOf  string `json:"asOf"`
		Lanes []struct {
			Coordinator        map[string]any   `json:"coordinator"`
			CoordinatorRetired bool             `json:"coordinatorRetired"`
			CountsByRole       []map[string]any `json:"countsByRole"`
			CountsByRoleFamily []map[string]any `json:"countsByRoleFamily"`
			Workers            []map[string]any `json:"workers"`
		} `json:"lanes"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.AppID != "capp100000000000000000000" || got.AsOf != "2026-10-26T06:00:00Z" {
		t.Errorf("snapshot metadata lost: %s %s", got.AppID, got.AsOf)
	}
	if len(got.Lanes) != 3 {
		t.Fatalf("lanes: %d", len(got.Lanes))
	}
	if got.Lanes[0].Coordinator["urn"] != "hrn:worker:acme.com:eng-team:eden" || got.Lanes[0].CoordinatorRetired {
		t.Errorf("lane 0 coordinator: %v", got.Lanes[0])
	}
	if !got.Lanes[1].CoordinatorRetired || got.Lanes[1].Coordinator["name"] != "Gone" {
		t.Errorf("a retired coordinator stays named and flagged: %v", got.Lanes[1])
	}
	if got.Lanes[2].Coordinator != nil {
		t.Errorf("the unassigned lane keeps a null coordinator: %v", got.Lanes[2].Coordinator)
	}
	if got.Lanes[1].Workers == nil || got.Lanes[1].CountsByRole == nil || got.Lanes[1].CountsByRoleFamily == nil {
		t.Errorf("empty lists render as [], not null: %s", out)
	}
	if len(got.Lanes[0].CountsByRole) != 2 || got.Lanes[0].CountsByRole[1]["role"] != nil {
		t.Errorf("a null role is its own bucket: %v", got.Lanes[0].CountsByRole)
	}
	if len(got.Lanes[0].CountsByRoleFamily) != 3 || got.Lanes[0].CountsByRoleFamily[1]["family"] != nil {
		t.Errorf("a null family is its own bucket: %v", got.Lanes[0].CountsByRoleFamily)
	}
	if len(got.Lanes[0].Workers) != 3 || got.Lanes[0].Workers[0]["worker"].(map[string]any)["id"] != "w-jack" {
		t.Errorf("worker order is the server's: %v", got.Lanes[0].Workers)
	}
}

func TestTeamCompositionTextRendersLanesAsReturned(t *testing.T) {
	_, out, err := runTeamTask(t, compositionResponses1085(composition1085), "team", "composition")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Eden", "Gone (retired)", "unassigned", "Jack", "Cedric", "engineer", "claude", "glm", "as of 2026-10-26T06:00:00Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("text missing %q:\n%s", want, out)
		}
	}
	if i, j, k := strings.Index(out, "Eden"), strings.Index(out, "Gone"), strings.Index(out, "unassigned"); !(i < j && j < k) {
		t.Errorf("lanes keep the server's order:\n%s", out)
	}
}

func TestTeamCompositionRefusedReadIsAFailureNotAnEmptyTeam(t *testing.T) {
	cases := map[string]string{
		"refused":     `{"errors":[{"message":"not allowed","extensions":{"code":"FORBIDDEN"}}],"data":null}`,
		"unsupported": `{"errors":[{"message":"Cannot query field \"teamComposition\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
		"null result": `{"data":{"teamComposition":null}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			for _, args := range [][]string{{"team", "composition"}, {"team", "composition", "--json"}} {
				_, out, err := runTeamTask(t, compositionResponses1085(body), args...)
				if err == nil {
					t.Fatalf("%v must fail, got success:\n%s", args, out)
				}
				if strings.Contains(out, "lanes") || strings.Contains(out, "no lanes") {
					t.Errorf("a failure must not render a team:\n%s", out)
				}
			}
		})
	}
}

func TestTeamCompositionEmptyTeamIsAnAnswer(t *testing.T) {
	_, out, err := runTeamTask(t, compositionResponses1085(`{"data":{"teamComposition":{"appId":"a","asOf":"2026-10-26T06:00:00Z","lanes":[]}}}`), "team", "composition", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"lanes": []`) {
		t.Errorf("a server-confirmed empty team renders lanes []:\n%s", out)
	}
}

func TestTeamRosterTextShowsReportsTo(t *testing.T) {
	row := func(name, reports string, retired string) string {
		r := rosterRow993(name, "AVAILABLE", true, `"m"`, `"high"`)
		return strings.Replace(r, `"reportsTo":null,"reportsToRetired":false`, `"reportsTo":`+reports+`,"reportsToRetired":`+retired, 1)
	}
	_, out, err := runTeamTask(t, rosterResponses993(
		row("Ann", `{"id":"w-eden","kind":"WORKER","name":"Eden","urn":"hrn:worker:acme.com:eng-team:eden"}`, "false"),
		row("Bo", `{"id":"w-gone","kind":"WORKER","name":"Gone","urn":null}`, "true"),
		row("Cy", "null", "false"),
	), "team", "roster")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"REPORTS TO", "Eden", "Gone (retired)"} {
		if !strings.Contains(out, want) {
			t.Errorf("roster text missing %q:\n%s", want, out)
		}
	}
}
