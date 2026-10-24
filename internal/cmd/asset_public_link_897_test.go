package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestAssetPublicLink897(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, available := range []bool{false, true} {
			for _, asJSON := range []bool{false, true} {
				t.Run(fmt.Sprintf("enabled%v/available%v/json%v", enabled, available, asJSON), func(t *testing.T) {
					var mu sync.Mutex
					calls := 0
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var req struct {
							OperationName string
							Variables     map[string]any
						}
						_ = json.NewDecoder(r.Body).Decode(&req)
						mu.Lock()
						calls++
						mu.Unlock()
						if req.OperationName != "SetAssetPublicLink" || req.Variables["assetRef"] != "a1" || req.Variables["enabled"] != enabled || len(req.Variables) != 2 {
							t.Errorf("unexpected write: %+v", req)
						}
						fmt.Fprintf(w, `{"data":{"setAssetPublicLink":{"id":"a1","publicLinkEnabled":%v,"canManagePublicLink":true,"publicLinkAvailable":%v,"publicUrl":null}}}`, enabled, available)
					}))
					defer srv.Close()
					f, out := testFactory(t)
					root := NewRootCmd(f)
					flag := "--off"
					if enabled {
						flag = "--on"
					}
					args := []string{"asset", "public-link", "hrn:asset:acme.com:kb:assets:a1", flag, "--server", srv.URL}
					if enabled {
						args = append(args, "--yes")
					}
					if asJSON {
						args = append(args, "--json")
					}
					root.SetArgs(args)
					if err := root.Execute(); err != nil {
						t.Fatal(err)
					}
					mu.Lock()
					n := calls
					mu.Unlock()
					if n != 1 {
						t.Fatalf("HTTP calls=%d", n)
					}
					if asJSON {
						var dto map[string]any
						if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
							t.Fatal(err)
						}
						if dto["publicLinkEnabled"] != enabled || dto["publicLinkAvailable"] != available || dto["canManagePublicLink"] != true || dto["publicUrl"] != nil {
							t.Fatalf("readback %s", out)
						}
					} else {
						want := "public link: off"
						if enabled {
							want = "no public URL on this server"
							if !available {
								want = "serving disabled on this deployment"
							}
						}
						if !strings.Contains(out.String(), want) {
							t.Fatalf("%q missing %q", out.String(), want)
						}
					}
				})
			}
		}
	}
}
func TestAssetPublicLink897UsageNoRequests(t *testing.T) {
	for _, flags := range [][]string{nil, {"--on", "--off"}, {"--on"}, {"--on=false"}, {"--off=false"}} {
		t.Run(fmt.Sprint(flags), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected HTTP") }))
			defer srv.Close()
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := append([]string{"asset", "public-link", "a1", "--server", srv.URL}, flags...)
			root.SetArgs(args)
			if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
				t.Fatalf("exit=%d", got)
			}
		})
	}
}
func TestAssetPublicLink897Refusals(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{{"FORBIDDEN", 8}, {"PERMISSION_DENIED", 8}, {"NOT_FOUND", 4}, {"GRAPHQL_VALIDATION_FAILED", 2}} {
		t.Run(tc.code, func(t *testing.T) {
			gql := fakeGraphQL(t, map[string]string{"SetAssetPublicLink": `{"errors":[{"message":"server refusal sentinel","extensions":{"code":"` + tc.code + `"}}]}`})
			defer gql.Close()
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"asset", "public-link", "a1", "--off", "--server", gql.URL})
			err := root.Execute()
			if exitCodeFor(err) != tc.want {
				t.Fatalf("%v", err)
			}
			if (tc.code == "FORBIDDEN" || tc.code == "PERMISSION_DENIED") && !strings.Contains(err.Error(), "server refusal sentinel") {
				t.Fatal(err)
			}
		})
	}
}
func TestAssetPublicLink897ListReadback(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/%v", enabled, asJSON), func(t *testing.T) {
				row := strings.TrimSuffix(assetJSON("a1", "logo.png", "image/png", 7, "CLEAN", "null"), "}") + fmt.Sprintf(`,"publicLinkEnabled":%v,"canManagePublicLink":false,"publicLinkAvailable":true}`, enabled)
				gql := fakeGraphQL(t, map[string]string{"MemoryAssets": assetListResp(1, false, row)})
				defer gql.Close()
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"asset", "ls", "-m", "mem1", "--server", gql.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if asJSON {
					var dto struct{ Assets []map[string]any }
					if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
						t.Fatal(err)
					}
					a := dto.Assets[0]
					if a["publicLinkEnabled"] != enabled || a["canManagePublicLink"] != false || a["publicLinkAvailable"] != true || a["publicUrl"] != nil {
						t.Fatal(out.String())
					}
				} else {
					if !strings.Contains(out.String(), "PUBLIC LINK") {
						t.Fatal(out.String())
					}
				}
			})
		}
	}
}

func TestAssetPublicLink897Confirmation(t *testing.T) {
	for _, answer := range []string{"yes\n", "no\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			var mu sync.Mutex
			calls := 0
			gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls++
				mu.Unlock()
				fmt.Fprint(w, `{"data":{"setAssetPublicLink":{"id":"a1","publicLinkEnabled":true,"canManagePublicLink":true,"publicLinkAvailable":true,"publicUrl":"https://example.test/assets/a1"}}}`)
			}))
			defer gql.Close()
			f, out, stderr := testFactoryTTY(t, answer)
			root := NewRootCmd(f)
			root.SetArgs([]string{"asset", "public-link", "a1", "--on", "--server", gql.URL})
			err := root.Execute()
			if !strings.Contains(stderr.String(), "anyone with the link") {
				t.Fatal(stderr.String())
			}
			mu.Lock()
			n := calls
			mu.Unlock()
			if answer == "yes\n" {
				if err != nil || n != 1 || !strings.Contains(out.String(), "https://example.test/assets/a1") {
					t.Fatalf("%v calls%d out%s", err, n, out)
				}
			} else if exitCodeFor(err) != exitcode.Cancelled || n != 0 {
				t.Fatalf("%v calls%d", err, n)
			}
		})
	}
}
func TestAssetPublicLink897URLState(t *testing.T) {
	for _, tc := range []struct {
		enabled, available bool
		want               string
	}{{false, true, "public link: off"}, {true, false, "serving disabled"}, {true, true, "no public URL on this server"}} {
		t.Run(tc.want, func(t *testing.T) {
			row := strings.TrimSuffix(assetJSON("a1", "logo.png", "image/png", 7, "CLEAN", "null"), "}") + fmt.Sprintf(`,"publicLinkEnabled":%v,"canManagePublicLink":false,"publicLinkAvailable":%v}`, tc.enabled, tc.available)
			gql := fakeGraphQL(t, map[string]string{"MemoryAssets": assetListResp(1, false, row)})
			defer gql.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"asset", "url", "a1", "-m", "mem1", "--json", "--server", gql.URL})
			err := root.Execute()
			if exitCodeFor(err) != exitcode.Conflict {
				t.Fatal(err)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if dto["publicLinkEnabled"] != tc.enabled || dto["publicLinkAvailable"] != tc.available || !strings.Contains(fmt.Sprint(dto["reason"]), tc.want) {
				t.Fatal(out.String())
			}
		})
	}
}

func TestAssetPublicLink897Selections(t *testing.T) {
	for _, doc := range []string{gen.MemoryAssets_Operation, gen.SetAssetPublicLink_Operation} {
		for _, field := range []string{"publicLinkEnabled", "canManagePublicLink", "publicLinkAvailable", "publicUrl"} {
			found := false
			for _, line := range strings.Split(doc, "\n") {
				if strings.TrimSpace(line) == field {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing selected field %s", field)
			}
		}
	}
	raw, err := json.Marshal(gen.RegisterHostInput{})
	if err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	_ = json.Unmarshal(raw, &vars)
	if _, present := vars["capabilities"]; present {
		t.Fatalf("schema-refresh capability must stay omitted: %s", raw)
	}
}
