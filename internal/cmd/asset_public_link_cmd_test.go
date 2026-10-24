package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func publicLinkRow(enabled, available, capable bool) string {
	row := assetJSON("a1", "one.png", "image/png", 10, "CLEAN", "null")
	var value map[string]any
	_ = json.Unmarshal([]byte(row), &value)
	value["publicLinkEnabled"] = enabled
	value["publicLinkAvailable"] = available
	value["canManagePublicLink"] = capable
	b, _ := json.Marshal(value)
	return string(b)
}

func TestAssetPublicLinkToggle(t *testing.T) {
	for _, on := range []bool{true, false} {
		t.Run(map[bool]string{true: "on", false: "off"}[on], func(t *testing.T) {
			gql, requests := captureGraphQL(t, map[string]string{"SetAssetPublicLink": `{"data":{"setAssetPublicLink":` + publicLinkRow(on, false, true) + `}}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			flag := "--off"
			if on {
				flag = "--on"
			}
			args := []string{"asset", "public-link", "hrn:asset:acme.com:kb:assets:a1", flag, "--server", gql.URL, "--json"}
			if on {
				args = append(args, "--yes")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			vars := decodeVars(t, requests, "SetAssetPublicLink")
			if vars["assetRef"] != "a1" || vars["enabled"] != on {
				t.Fatalf("variables: %#v", vars)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if dto["publicLinkEnabled"] != on || dto["publicLinkAvailable"] != false || dto["canManagePublicLink"] != true {
				t.Fatalf("DTO: %s", out)
			}
		})
	}
}

func TestAssetPublicLinkInvalidFlagsAndConfirmationSendNothing(t *testing.T) {
	for _, flags := range [][]string{nil, {"--on", "--off"}, {"--on"}} {
		gql, requests := captureGraphQL(t, map[string]string{})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		args := append([]string{"asset", "public-link", "a1", "--server", gql.URL}, flags...)
		root.SetArgs(args)
		if err := root.Execute(); exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("%v: %v", flags, err)
		}
		if len(requests) != 0 {
			t.Fatal("invalid/unconfirmed input made a request")
		}
	}
}

func TestAssetPublicLinkReadback(t *testing.T) {
	for _, tc := range []struct {
		enabled, available bool
		text               string
	}{
		{false, true, "public link: off"}, {true, false, "serving disabled on this deployment"}, {true, true, "no public URL on this server"},
	} {
		for _, command := range []string{"list", "url"} {
			for _, jsonMode := range []bool{true, false} {
				gql := fakeGraphQL(t, map[string]string{"MemoryAssets": assetListResp(1, false, publicLinkRow(tc.enabled, tc.available, false))})
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"asset", command}
				if command == "url" {
					args = append(args, "a1")
				}
				args = append(args, "-m", "mem1", "--server", gql.URL)
				if jsonMode {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				err := root.Execute()
				if command == "list" && err != nil {
					t.Fatal(err)
				}
				if command == "url" && exitCodeFor(err) != exitcode.Conflict {
					t.Fatalf("absent URL: %v", err)
				}
				if jsonMode {
					var dto map[string]any
					_ = json.Unmarshal([]byte(out.String()), &dto)
					if command == "list" {
						dto = dto["assets"].([]any)[0].(map[string]any)
					}
					if dto["publicLinkEnabled"] != tc.enabled || dto["publicLinkAvailable"] != tc.available || dto["canManagePublicLink"] != false {
						t.Fatalf("missing/additive fields: %s", out)
					}
				} else if !strings.Contains(out.String(), tc.text) {
					t.Fatalf("want %q: %s", tc.text, out)
				}
			}
		}
	}
}

func TestAssetPublicLinkRefusals(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{{"FORBIDDEN", exitcode.Forbidden}, {"NOT_FOUND", exitcode.NotFound}, {"GRAPHQL_VALIDATION_FAILED", exitcode.Usage}} {
		message := "server refusal"
		if tc.code == "GRAPHQL_VALIDATION_FAILED" {
			message = `Cannot query field "setAssetPublicLink" on type "Mutation".`
		}
		b, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]any{"code": tc.code}}}})
		gql := fakeGraphQL(t, map[string]string{"SetAssetPublicLink": string(b)})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"asset", "public-link", "a1", "--off", "--server", gql.URL})
		err := root.Execute()
		if exitCodeFor(err) != tc.exit {
			t.Fatalf("%s: %v (%d)", tc.code, err, exitCodeFor(err))
		}
	}
}
