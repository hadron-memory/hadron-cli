package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestTaskMinTier1479Create(t *testing.T) {
	for _, tier := range []string{"low", "mid", "high", ""} {
		t.Run(tier, func(t *testing.T) {
			args := []string{"team", "task", "create", "Tier task", "--require-role", "cli-engineer"}
			if tier != "" {
				args = append(args, "--min-tier", tier)
			}
			calls, _, err := runTeamTask(t, taskResponses(), args...)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for _, c := range *calls {
				if c.Op != "AgentTaskCreate" {
					continue
				}
				n++
				input := c.Vars["input"].(map[string]any)
				req := input["requirements"].(map[string]any)
				got, exists := req["minTier"]
				if tier == "" {
					if exists {
						t.Fatalf("absent tier sent: %#v", req)
					}
				} else if got != tier {
					t.Fatalf("tier %#v, want %s", req, tier)
				}
			}
			if n != 1 {
				t.Fatalf("creates %d", n)
			}
		})
	}
}

func TestTaskMinTier1479Update(t *testing.T) {
	for _, withRole := range []bool{false, true} {
		args := []string{"task1", "--min-tier", "high"}
		if withRole {
			args = append(args, "--require-role", "cli-engineer")
		}
		input, _ := updateInput1137(t, args...)
		var req map[string]any
		if err := json.Unmarshal(input["requirements"], &req); err != nil {
			t.Fatal(err)
		}
		want := 1
		if withRole {
			want++
			if req["role"] != "cli-engineer" {
				t.Fatal(req)
			}
		}
		if req["minTier"] != "high" || len(req) != want || len(input) != 1 {
			t.Fatalf("replacement %#v input %s", req, input)
		}
	}
	input, _ := updateInput1137(t, "task1", "--clear-requirements")
	if string(input["requirements"]) != "null" {
		t.Fatal(input)
	}
}

func TestTaskMinTier1479UsageBeforeHTTP(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		for _, tier := range []string{"", "xyz", "MID", " mid", "mid "} {
			calls, _, err := runTeamTask(t, taskResponses(), "team", "task", verb, "task1", "--min-tier="+tier)
			if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
				t.Fatalf("%s %q: err %v calls %#v", verb, tier, err, *calls)
			}
		}
	}
	calls, _, err := runTeamTask(t, taskUpdateResponses1137(), "team", "task", "update", "task1", "--min-tier", "mid", "--clear-requirements")
	if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 || !strings.Contains(err.Error(), "contradict") {
		t.Fatalf("conflict: %v calls %#v", err, *calls)
	}
}

func TestTaskMinTier1479Read(t *testing.T) {
	for _, tier := range []string{"mid", ""} {
		for _, verb := range []string{"get", "list"} {
			for _, asJSON := range []bool{false, true} {
				r := taskResponses()
				if tier != "" {
					for k, v := range r {
						r[k] = strings.ReplaceAll(v, `"requirements":{"role"`, `"requirements":{"minTier":"mid","role"`)
					}
				}
				args := []string{"team", "task", verb}
				if verb == "get" {
					args = append(args, "task1")
				}
				if asJSON {
					args = append(args, "--json")
				}
				calls, out, err := runTeamTask(t, r, args...)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range *calls {
					if c.Op == "AgentTaskGet" || c.Op == "AgentTaskList" {
						if !strings.Contains(c.Query, "requirements {\n\t\tminTier") {
							t.Fatalf("tier not selected in %s", c.Query)
						}
					}
				}
				if asJSON {
					var task map[string]any
					if verb == "get" {
						if err := json.Unmarshal([]byte(out), &task); err != nil {
							t.Fatal(err)
						}
					} else {
						var tasks []map[string]any
						if err := json.Unmarshal([]byte(out), &tasks); err != nil {
							t.Fatal(err)
						}
						task = tasks[0]
					}
					req := task["requirements"].(map[string]any)
					v, ok := req["minTier"]
					if !ok || (tier == "" && v != nil) || (tier != "" && v != tier) {
						t.Fatalf("%s tier %q: %#v", verb, tier, req)
					}
				} else if tier != "" {
					if !strings.Contains(out, "mid") {
						t.Fatalf("%s missing tier: %s", verb, out)
					}
				} else if strings.Contains(out, "minTier") || strings.Contains(out, "MIN TIER") {
					t.Fatalf("absent tier shown: %s", out)
				}
			}
		}
	}
}

func TestTaskMinTier1479ServerRefusal(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		r := taskUpdateResponses1137()
		op := "AgentTaskCreate"
		if verb == "update" {
			op = "AgentTaskUpdate"
		}
		r[op] = `{"errors":[{"message":"minimum tier rejected by server","extensions":{"code":"BAD_USER_INPUT"}}]}`
		calls, _, err := runTeamTask(t, r, "team", "task", verb, "task1", "--min-tier", "mid")
		n := 0
		for _, c := range *calls {
			if c.Op == op {
				n++
			}
		}
		if n != 1 || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "minimum tier rejected by server") {
			t.Fatalf("%s n%d: %v", verb, n, err)
		}
	}
}
