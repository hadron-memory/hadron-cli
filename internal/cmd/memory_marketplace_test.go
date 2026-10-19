package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#592 (cor:acl:080:04): `memory set --listed-on-marketplace` forwards
// the opt-in catalogue flag, and `memory get` shows the server's value. The
// CLI is a thin client: the server owns the listing and access policy.

func memoryListedJSON(listed bool) string {
	v := "false"
	if listed {
		v = "true"
	}
	return `{"listedOnMarketplace":` + v + `,` + strings.TrimPrefix(memoryJSON, "{")
}

func runMemorySet(t *testing.T, args ...string) (map[string]any, string, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{
		"GetMemory":    `{"data":{"memory":` + memoryListedJSON(true) + `}}`,
		"UpdateMemory": `{"data":{"updateMemory":` + memoryListedJSON(true) + `}}`,
		"CreateMemory": `{"data":{"createMemory":` + memoryJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"memory", "set"}, args...), "--server", gql.URL))
	err := root.Execute()
	var vars map[string]any
	if raw, ok := captured["UpdateMemory"]; ok {
		_ = json.Unmarshal(raw, &vars)
	}
	return vars, out.String(), err
}

func TestMemorySetListedOnMarketplaceForwardsExplicitValues(t *testing.T) {
	for _, c := range []struct {
		flag string
		want bool
	}{
		{"--listed-on-marketplace", true},
		{"--listed-on-marketplace=true", true},
		{"--listed-on-marketplace=false", false},
	} {
		t.Run(c.flag, func(t *testing.T) {
			vars, _, err := runMemorySet(t, "acme.com::kb", c.flag)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			got, present := vars["listedOnMarketplace"]
			if !present || got != c.want {
				t.Errorf("listedOnMarketplace = %v (present %v), want %v; vars %v", got, present, c.want, vars)
			}
		})
	}
}

// Omitted means preserve: sending false unasked would silently de-list.
func TestMemorySetWithoutTheFlagOmitsListedOnMarketplace(t *testing.T) {
	vars, _, err := runMemorySet(t, "acme.com::kb", "--short", "x")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if v, present := vars["listedOnMarketplace"]; present {
		t.Errorf("an omitted flag must not reach the wire, got %v", v)
	}
}

// The flag alone is a valid update, not "nothing to update".
func TestMemorySetListedOnMarketplaceAloneIsAnUpdate(t *testing.T) {
	vars, out, err := runMemorySet(t, "acme.com::kb", "--listed-on-marketplace=false", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if vars["listedOnMarketplace"] != false {
		t.Errorf("vars: %v", vars)
	}
	// The update's receipt carries the server's value.
	if !strings.Contains(out, `"listedOnMarketplace": true`) {
		t.Errorf("the update receipt must report the server's value:\n%s", out)
	}
}

// Listing is a deliberate, later act: not offered on create.
func TestMemorySetListedOnMarketplaceRefusedOnCreate(t *testing.T) {
	_, _, err := runMemorySet(t, "--org", "acme.com", "--name", "KB", "--listed-on-marketplace")
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Errorf("exit = %d, want %d; err: %v", code, exitcode.Usage, err)
	}
	if err == nil || !strings.Contains(err.Error(), "--listed-on-marketplace") {
		t.Errorf("the refusal must name the flag: %v", err)
	}
}

// A server refusal keeps its mapped exit code: the server owns the policy.
func TestMemorySetListedOnMarketplaceServerRefusalIsMapped(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetMemory":    `{"data":{"memory":` + memoryJSON + `}}`,
		"UpdateMemory": `{"errors":[{"message":"not allowed","extensions":{"code":"FORBIDDEN"}}],"data":null}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "set", "acme.com::kb", "--listed-on-marketplace", "--server", gql.URL})
	if code := exitCodeFor(root.Execute()); code != exitcode.Forbidden {
		t.Errorf("exit = %d, want %d (Forbidden)", code, exitcode.Forbidden)
	}
}

func TestMemoryGetShowsListedOnMarketplace(t *testing.T) {
	for _, listed := range []bool{true, false} {
		gql, _ := captureGraphQL(t, map[string]string{
			"GetMemory": `{"data":{"memory":` + memoryListedJSON(listed) + `}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"memory", "get", "acme.com::kb", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		want := "marketplace: not listed"
		if listed {
			want = "marketplace: listed"
		}
		if !strings.Contains(out.String(), want) {
			t.Errorf("human get must show %q:\n%s", want, out.String())
		}

		f2, out2 := testFactory(t)
		root2 := NewRootCmd(f2)
		root2.SetArgs([]string{"memory", "get", "acme.com::kb", "--json", "--server", gql.URL})
		if err := root2.Execute(); err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out2.String()), &dto); err != nil {
			t.Fatal(err)
		}
		if dto["listedOnMarketplace"] != listed {
			t.Errorf("--json listedOnMarketplace = %v, want %v", dto["listedOnMarketplace"], listed)
		}
	}
}
