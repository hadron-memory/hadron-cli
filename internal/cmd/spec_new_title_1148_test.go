package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func spec1148Server(t *testing.T) (string, map[string]json.RawMessage) {
	t.Helper()
	server, captured := captureGraphQL(t, map[string]string{
		"FindNodes":      `{"data":{"nodes":[` + specNodeList("qat", `["spec"]`) + `,` + specNodeList("qat:api", `["spec"]`) + `,` + specNodeList("qat:api:010", `["spec"]`) + `]}}`,
		"ResolveUrn":     `{"errors":[{"message":"not found","extensions":{"code":"NOT_FOUND"}}]}`,
		"CreateSpecNode": `{"data":{"createSpecNode":{"id":"new-spec","memoryId":"mem1","loc":"qa:api:010:04","name":"created","nodeType":"info","tags":["spec"],"updatedAt":"2026-10-30T00:00:00Z"}}}`,
	})
	return server.URL, captured
}

func TestSpecNew1148RefusesComputedCitationPrefix(t *testing.T) {
	for _, tc := range []struct {
		name, title string
		args        []string
	}{
		{"original report", "qa:api:010:04 — Burst allowance", []string{"qa:api:010:04"}},
		{"leading whitespace", "  qa:api:010:04 — Burst allowance  ", []string{"qa:api:010:04"}},
		{"legacy allocation", "qat:api:010:04 — Burst allowance", []string{"--product", "qat", "--module", "api", "--feature", "010", "--rule", "04"}},
		{"new path", "msg:010:04 — Burst allowance", []string{"msg:010:04", "--new-path"}},
	} {
		for _, dry := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: " write", true: " preview"}[dry], func(t *testing.T) {
				url, captured := spec1148Server(t)
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := append([]string{"spec", "new"}, tc.args...)
				args = append(args, "-m", specMem, "--title", tc.title, "--no-edges", "--json", "--server", url)
				if dry {
					args = append(args, "--dry-run")
				}
				root.SetArgs(args)
				err := root.Execute()
				if exitCodeFor(err) != 2 {
					t.Fatalf("want usage2 for copied full name; got %v; output %s", err, out.String())
				}
				if err == nil || !strings.Contains(err.Error(), "added automatically") || !strings.Contains(err.Error(), "drop it") {
					t.Fatalf("missing remedy: %v", err)
				}
				for _, op := range []string{"CreateSpecNode", "CreateNode", "UpdateSpecNode", "CreateEdge"} {
					if _, ok := captured[op]; ok {
						t.Fatalf("refusal wrote via %s", op)
					}
				}
				if out.String() != "" {
					t.Fatalf("refusal printed success: %s", out.String())
				}
			})
		}
	}
}

func TestSpecNew1148PlainAndLiteralTitleControls(t *testing.T) {
	for _, tc := range []struct {
		name, title, want string
		role              bool
	}{
		{"plain title", "Burst allowance", "qa:api:010:04 — Burst allowance", false},
		{"different citation", "qa:api:010:03 — Related rule", "qa:api:010:04 — qa:api:010:03 — Related rule", false},
		{"longer loc prefix", "qa:api:010:040 — Related rule", "qa:api:010:04 — qa:api:010:040 — Related rule", false},
		{"explicit role literal", "qa:api:010:04 — Burst allowance", "qa:api:010:04 — Burst allowance", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, captured := spec1148Server(t)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"spec", "new", "qa:api:010:04", "-m", specMem, "--title", tc.title, "--no-edges", "--json", "--server", url}
			if tc.role {
				args = append(args, "--role", "spec.rule")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var in sentSpecInput
			if err := json.Unmarshal(captured["CreateSpecNode"], &in); err != nil {
				t.Fatal(err)
			}
			var result struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
				t.Fatal(err)
			}
			if in.Input.Name != tc.want || result.Name != tc.want {
				t.Fatalf("payload/output changed: %q/%q want %q", in.Input.Name, result.Name, tc.want)
			}
		})
	}
}

// The help guidance pin moved to TestSpecNewTitleHelpGuidance (#1302), which
// compares the help with its whitespace collapsed.
