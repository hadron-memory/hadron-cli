package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// #1112: each case identifies a particular output site and interpolation.
// JSON templates are captured with the baseline renderer before conversion.
// The report identified raw render sites without an exact hostile payload;
// controls and long values below are synthetic probes, not a replayed incident.
func TestTeamMetadata1112(t *testing.T) {
	type site struct {
		name             string
		args             []string
		op, path, marker string
		suppress         bool
	}
	// C6 removes the retired template-probe warning site. Role interpolation
	// remains covered by role-missing-role; its active warning is unchanged.
	sites := []site{
		{"role-missing-role", []string{"role", "list"}, "TeamRoles", "data/teamRoles/items/1/role", "no installed agent", false},
		{"role-reverse-name", []string{"role", "list"}, "AppAgentRoster", "data/app/agents/1/name", "no matching role definition", false},
		{"role-reverse-role", []string{"role", "list"}, "AppAgentRoster", "data/app/agents/1/personaRole", "no matching role definition", true},
		{"composition-asof", []string{"composition"}, "TeamComposition", "data/teamComposition/asOf", "as of ", false},
		{"roster-asof", []string{"roster"}, "TeamRoster", "data/teamRoster/asOf", "as of ", false},
		{"attention-token", []string{"attention"}, "TeamAttentionPage", "data/teamAttentionPage/adoptableSince", "token:", true},
		{"preview-expiry", []string{"attention", "switchover", "preview"}, "TeamAttentionPreviewPage", "data/teamAttentionPreviewPage/expiresAt", "Nothing has been written", false},
		{"preview-proof", []string{"attention", "switchover", "preview"}, "TeamAttentionPreviewPage", "data/teamAttentionPreviewPage/proof", "", true},
		{"host-cursor", []string{"host", "ops"}, "HostOperations", "data/hostOperations/endCursor", "", true},
		{"stop-lift-changed", []string{"stop", "lift", "sig2"}, "TeamStopSignalLift", "data/liftTeamStopSignal/signal/id", "Lifted stop signal", false},
		{"stop-lift-ended", []string{"stop", "lift", "sig2"}, "TeamStopSignalLift", "data/liftTeamStopSignal/signal/id", "had already ended", false},
	}
	variants := []struct{ name, value, want string }{
		{"clean", "fixture", "fixture"},
		{"controls", "a\nb\x1b[31mc\u009b\u007f\u202e\u2028\u2029\u200b", "a b [31mc      "},
		{"long", strings.Repeat("界", 1000), strings.Repeat("界", 255) + "…"},
	}
	for _, site := range sites {
		for _, v := range variants {
			t.Run(site.name+"/"+v.name, func(t *testing.T) {
				responses := hostResponses()
				for op, body := range stopResponses() {
					responses[op] = body
				}
				responses["TeamRoles"] = teamRolesJSON
				responses["AppAgentRoster"] = engTeamRosterJSON
				responses["TeamComposition"] = composition1085
				responses["TeamAttentionPage"] = attentionJSON
				responses["TeamAttentionPreviewPage"] = switchoverPreviewJSON
				if site.name == "stop-lift-ended" {
					responses[site.op] = strings.Replace(responses[site.op], `"changed":true`, `"changed":false`, 1)
				}
				body := replaceMetadataLeaf1112(t, responses[site.op], site.path, v.value)
				responses[site.op] = body
				args := append([]string{"team"}, site.args...)
				_, raw, err := runTeamTask(t, responses, append(args, "--json")...)
				if err != nil {
					t.Fatal(err)
				}
				metadataJSON1112(t, site.name+"-"+v.name, raw, "")
				_, out, err := runTeamTask(t, responses, args...)
				if err != nil {
					t.Fatal(err)
				}
				// Select the warning/footer site, not a sibling table that already had a guard.
				selected := out
				if site.marker != "" {
					selected = ""
					for _, line := range strings.Split(out, "\n") {
						if strings.Contains(line, site.marker) {
							selected += line + "\n"
						}
					}
					if selected == "" {
						t.Fatalf("site marker %q not rendered", site.marker)
					}
				}
				if !strings.Contains(selected, v.want) {
					t.Errorf("site did not render its bounded sanitized value")
				}
				for _, r := range out {
					if r != '\n' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)) {
						t.Errorf("raw terminal character U+%04X", r)
						break
					}
				}
				if strings.Contains(out, strings.Repeat("界", 256)) {
					t.Error("uncut long value survived")
				}
				if v.name == "clean" {
					metadataText1112(t, site.name, out, "")
				}
				if site.suppress && v.name != "clean" {
					if !strings.Contains(out, "--json") {
						t.Error("altered value needs JSON guidance")
					}
					switch site.name {
					case "role-reverse-role":
						if strings.Contains(out, "create one with `hadron team role create") {
							t.Error("altered role offered as a command")
						}
					case "preview-proof":
						if strings.Contains(out, "hadron team attention switchover apply --app") {
							t.Error("altered proof offered as an apply command")
						}
					case "host-cursor":
						if strings.Contains(out, "continue with --after") {
							t.Error("altered cursor offered as a flag argument")
						}
					case "attention-token":
						if strings.Contains(out, "Pass it as --since") {
							t.Error("altered token offered as reusable")
						}
					}
				}
			})
		}
	}
}

func replaceMetadataLeaf1112(t *testing.T, body, path, value string) string {
	t.Helper()
	var tree any
	if err := json.Unmarshal([]byte(body), &tree); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(path, "/")
	current := tree
	for _, part := range parts[:len(parts)-1] {
		switch x := current.(type) {
		case map[string]any:
			current = x[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil {
				t.Fatal(err)
			}
			current = x[i]
		default:
			t.Fatalf("missing fixture path %s", path)
		}
	}
	leaf := parts[len(parts)-1]
	switch x := current.(type) {
	case map[string]any:
		if _, ok := x[leaf]; !ok {
			t.Fatalf("missing leaf %s", path)
		}
		x[leaf] = value
	default:
		t.Fatalf("not an object at %s", path)
	}
	b, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func metadataJSON1112(t *testing.T, name, raw, fixtureDir string) {
	t.Helper()
	path := filepath.Join("testdata", "metadata-1112", name+".json")
	if os.Getenv("UPDATE_METADATA_1112") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		snapshot := raw
		if fixtureDir != "" {
			snapshot = strings.ReplaceAll(raw, fixtureDir, "__FIXTURE_DIR__")
		}
		if err := os.WriteFile(path, []byte(snapshot), 0644); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := string(saved)
	if fixtureDir != "" {
		expected = strings.ReplaceAll(expected, "__FIXTURE_DIR__", fixtureDir)
	}
	if raw != expected {
		t.Errorf("raw JSON bytes changed\nwant: %s\ngot: %s", expected, raw)
	}
}

func metadataText1112(t *testing.T, name, out, fixtureDir string) {
	t.Helper()
	path := filepath.Join("testdata", "metadata-1112", name+"-clean.txt")
	url := regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+`).FindString(out)
	if os.Getenv("UPDATE_METADATA_1112") == "1" {
		snapshot := out
		if url != "" {
			snapshot = strings.ReplaceAll(snapshot, url, "__SERVER_URL__")
		}
		if fixtureDir != "" {
			snapshot = strings.ReplaceAll(snapshot, fixtureDir, "__FIXTURE_DIR__")
		}
		if err := os.WriteFile(path, []byte(snapshot), 0644); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := string(saved)
	if url != "" {
		expected = strings.ReplaceAll(expected, "__SERVER_URL__", url)
	}
	if fixtureDir != "" {
		expected = strings.ReplaceAll(expected, "__FIXTURE_DIR__", fixtureDir)
	}
	if out != expected {
		t.Errorf("clean terminal bytes changed\nwant:%s\ngot:%s", expected, out)
	}
}

func TestTeamMetadataCopyQuoting1112(t *testing.T) {
	for _, kind := range []string{"role", "proof", "cursor"} {
		t.Run(kind, func(t *testing.T) {
			responses := hostResponses()
			args := []string{"team", "role", "list"}
			value := "role; echo NEVER"
			responses["TeamRoles"] = teamRolesJSON
			responses["AppAgentRoster"] = engTeamRosterJSON
			want := "create one with `hadron team role create 'role; echo NEVER'`"
			switch kind {
			case "role":
				responses["AppAgentRoster"] = replaceMetadataLeaf1112(t, responses["AppAgentRoster"], "data/app/agents/1/personaRole", value)
			case "proof":
				args = []string{"team", "attention", "switchover", "preview"}
				value = "p' $HOME; echo NEVER"
				responses["TeamAttentionPreviewPage"] = replaceMetadataLeaf1112(t, switchoverPreviewJSON, "data/teamAttentionPreviewPage/proof", value)
				want = "--proof 'p'\\'' $HOME; echo NEVER'"
			case "cursor":
				args = []string{"team", "host", "ops"}
				responses["HostOperations"] = replaceMetadataLeaf1112(t, responses["HostOperations"], "data/hostOperations/endCursor", value)
				want = "continue with --after 'role; echo NEVER'"
			}
			_, out, err := runTeamTask(t, responses, args...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, want) {
				t.Errorf("missing exact safely quoted suggestion %q:\n%s", want, out)
			}
			// Suggestions are asserted as bytes, never executed by the test.
		})
	}
}

// Follow the new warning's remedy on the CLI surface that owns installed metadata.
func TestTeamMetadataPersonaRoleRemedy1112(t *testing.T) {
	const role = "role\noriginal"
	responses := map[string]string{"AppAgentRoster": replaceMetadataLeaf1112(t, engTeamRosterJSON, "data/app/agents/1/personaRole", role)}
	_, raw, err := runTeamTask(t, responses, "app", "agent", "list", "hrn:app:acme.com:eng-team", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto []struct {
		PersonaRole *string `json:"personaRole"`
	}
	if err := json.Unmarshal([]byte(raw), &dto); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, member := range dto {
		if member.PersonaRole != nil && *member.PersonaRole == role {
			found = true
		}
	}
	if !found {
		t.Fatalf("suggested command did not retain original persona role: %s", raw)
	}
}
