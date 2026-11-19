package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Khan/genqlient/graphql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/filterreport"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func TestFindFilteredCommandRedaction953(t *testing.T) {
	wt := t.TempDir()
	if err := os.Mkdir(filepath.Join(wt, ".hadron-team"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".hadron-team", "hadron-team-session.json"), []byte(`{"workerId":"w1","server":"https://test"}`), 0600); err != nil {
		t.Fatal(err)
	}
	const secret = "SCREEN_AND_DRAFT_SECRET_953"
	for _, jsonMode := range []bool{false, true} {
		for _, unreadable := range []bool{false, true} {
			streams, out, errOut := output.Test()
			f := &cmdutil.Factory{IOStreams: streams, JSON: jsonMode, AppFlag: "hrn:app:acme.com:team", ServerFlag: "https://test"}
			calls := []string{}
			source := filterSources{procRoot: t.TempDir(), targets: func(context.Context, string, []string) ([]filterreport.Target, error) {
				return []filterreport.Target{{ID: "w1", Name: "Joost"}}, nil
			}}
			source.runner = func() (orcaterm.Runner, bool) {
				return func(_ context.Context, args []string) orcaterm.Result {
					if len(args) < 2 || args[0] != "terminal" || (args[1] != "list" && args[1] != "read") {
						t.Fatalf("forbidden call %v", args)
					}
					calls = append(calls, args[1])
					if args[1] == "list" {
						return orcaterm.Result{Stdout: `{"ok":true,"result":{"terminals":[{"handle":"term1","worktreePath":` + strconv.Quote(wt) + `}]}}`}
					}
					if unreadable {
						return orcaterm.Result{Code: 1, Stderr: secret, Stdout: secret}
					}
					b, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminal": map[string]any{"source": "screen", "tail": []string{"ⓘ This content can’t be shown " + secret, "  Apply for Daybreak: https://openai.com/form/", "› "}, "draft": secret}}})
					return orcaterm.Result{Stdout: string(b)}
				}, true
			}
			cmd := newCmdWorkerFindFilteredWithSources(f, source)
			cmd.SetArgs(nil)
			err := cmd.Execute()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String()+errOut.String(), secret) {
				t.Fatal("secret leaked")
			}
			want := "BLOCKED"
			if unreadable {
				want = "UNREADABLE"
			}
			if jsonMode {
				var rows []map[string]any
				if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 1 {
					t.Fatalf("json %v %s", err, out)
				}
				if len(rows[0]) != 6 || rows[0]["classification"] != want || rows[0]["agent_tree"] != "no_wrapper" {
					t.Fatal(rows)
				}
			} else {
				if !strings.Contains(out.String(), want) {
					t.Fatal(out.String())
				}
				footer := "The block is the provider's. Daybreak access or Claude capacity is the human's route."
				if strings.Contains(out.String(), footer) == unreadable {
					t.Fatal("wrong provider footer", out.String())
				}
			}
			if strings.Join(calls, ",") != "list,read" {
				t.Fatal(calls)
			}
		}
	}
}

func TestFindFilteredHostCheckPrecedesReads953(t *testing.T) {
	for _, host := range []bool{false, true} {
		streams, out, _ := output.Test()
		f := &cmdutil.Factory{IOStreams: streams}
		calls := 0
		source := filterSources{procRoot: "/must-not-be-read", targets: func(context.Context, string, []string) ([]filterreport.Target, error) {
			t.Fatal("roster read before refusal")
			return nil, errors.New("unreachable")
		}, runner: func() (orcaterm.Runner, bool) {
			return func(_ context.Context, args []string) orcaterm.Result {
				calls++
				return orcaterm.Result{Code: 1, Stderr: "SECRET"}
			}, host
		}}
		cmd := newCmdWorkerFindFilteredWithSources(f, source)
		cmd.SetArgs(nil)
		err := cmd.Execute()
		if err == nil || exitcode.FromError(err) != 2 || err.Error() != findFilteredHostRefusal || out.Len() != 0 {
			t.Fatalf("%v %q", err, out.String())
		}
		want := 0
		if host {
			want = 1
		}
		if calls != want {
			t.Fatal(calls, want)
		}
	}
}

func TestFindFilteredScanErrorRedacted953(t *testing.T) {
	streams, out, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: streams, AppFlag: "hrn:app:acme.com:team"}
	source := filterSources{targets: func(context.Context, string, []string) ([]filterreport.Target, error) {
		return nil, errors.New("SECRET_SCREEN")
	}, runner: func() (orcaterm.Runner, bool) {
		return func(context.Context, []string) orcaterm.Result {
			return orcaterm.Result{Stdout: `{"ok":true,"result":{"terminals":[]}}`}
		}, true
	}}
	cmd := newCmdWorkerFindFilteredWithSources(f, source)
	cmd.SetArgs(nil)
	err := cmd.Execute()
	if err == nil || exitcode.FromError(err) != 1 || strings.Contains(err.Error(), "SECRET") || out.Len() != 0 {
		t.Fatalf("%v %q", err, out.String())
	}
}

func TestFindFilteredRosterReadAvoidsBindingHook953(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "fake953")
	for _, named := range []bool{false, true} {
		t.Run(fmt.Sprint(named), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Operation string         `json:"operationName"`
					Query     string         `json:"query"`
					Variables map[string]any `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				want := "FilteredWorkerRoster953"
				if named {
					want = "FilteredWorkerTarget953"
				}
				if req.Operation != want || req.Variables["appRef"] != "hrn:app:test.com:team" || r.Header.Get("Authorization") != "Bearer fake953" || strings.Contains(req.Query, "prompt") {
					t.Errorf("unexpected request %+v", req)
					w.WriteHeader(400)
					return
				}
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if named {
					_, _ = w.Write([]byte(`{"data":{"worker":{"id":"w1","name":"Iris","appId":"app1","appUrn":"hrn:app:test.com:team"}}}`))
				} else {
					_, _ = w.Write([]byte(`{"data":{"workers":{"total":1,"items":[{"id":"w1","name":"Iris"}]}}}`))
				}
			}))
			defer server.Close()
			f := cmdutil.NewFactory()
			f.ServerFlag = server.URL
			f.HTTPClient = server.Client()
			f.GraphQLRequestContext = func(context.Context, *graphql.Request) context.Context {
				t.Fatal("read invoked the git-based authored-write snapshot")
				return context.Background()
			}
			args := []string{}
			if named {
				args = []string{"iris"}
			}
			targets, err := findFilteredTargets(context.Background(), f, "hrn:app:test.com:team", args)
			if err != nil || len(targets) != 1 || targets[0].ID != "w1" || calls.Load() != 1 {
				t.Fatal(targets, err, calls.Load())
			}
		})
	}
}

func TestFindFilteredEmptyJSON953(t *testing.T) {
	streams, out, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: streams, JSON: true, AppFlag: "hrn:app:test.com:team", ServerFlag: "https://test"}
	source := filterSources{procRoot: t.TempDir(), targets: func(context.Context, string, []string) ([]filterreport.Target, error) {
		return []filterreport.Target{}, nil
	}, runner: func() (orcaterm.Runner, bool) {
		return func(context.Context, []string) orcaterm.Result {
			return orcaterm.Result{Stdout: `{"ok":true,"result":{"terminals":[]}}`}
		}, true
	}}
	cmd := newCmdWorkerFindFilteredWithSources(f, source)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil || strings.TrimSpace(out.String()) != "[]" {
		t.Fatal(err, out.String())
	}
}

func TestFindFilteredTargetScope953(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "fake953")
	for _, tc := range []struct {
		name, arg, app, returnedApp string
		want                        int
		wantErr                     bool
	}{
		{"name same app", "Iris", "hrn:app:test.com:team", `"hrn:app:test.com:team"`, 1, false},
		{"explicit ref same app", "hrn:worker:test.com:team:iris", "hrn:app:test.com:team", `"hrn:app:test.com:team"`, 1, false},
		{"explicit ref legacy app", "hrn:worker:test.com:team:iris", "hrn:app:test.com:team", `"urn:app:test.com::team"`, 1, false},
		{"explicit ref other app", "hrn:worker:other.com:team:iris", "hrn:app:test.com:team", `"hrn:app:other.com:team"`, 0, false},
		{"legacy worker ref other app", "urn:worker:other.com:team:iris", "hrn:app:test.com:team", `"hrn:app:other.com:team"`, 0, false},
		{"name with foreign returned app", "Iris", "hrn:app:test.com:team", `"hrn:app:other.com:team"`, 0, false},
		{"explicit ref short app", "hrn:worker:test.com:team:iris", "test.com:team", `"hrn:app:test.com:team"`, 1, false},
		{"explicit ref legacy app input", "hrn:worker:test.com:team:iris", "urn:app:test.com::team", `"hrn:app:test.com:team"`, 1, false},
		{"name scope unavailable", "Iris", "hrn:app:test.com:team", "null", 0, true},
		{"explicit ref opaque app", "hrn:worker:test.com:team:iris", "0123456789abcdef0123456789abcdef", "null", 1, false},
		{"name opaque app", "Iris", "0123456789abcdef0123456789abcdef", "null", 1, false},
		{"explicit ref scope unavailable", "hrn:worker:test.com:team:iris", "hrn:app:test.com:team", "null", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"data":{"worker":{"id":"w1","name":"Iris","appId":"0123456789abcdef0123456789abcdef","appUrn":%s}}}`, tc.returnedApp)
			}))
			defer server.Close()
			f := cmdutil.NewFactory()
			f.ServerFlag = server.URL
			f.HTTPClient = server.Client()
			got, err := findFilteredTargets(context.Background(), f, tc.app, []string{tc.arg})
			if (err != nil) != tc.wantErr || len(got) != tc.want {
				t.Fatal(got, err)
			}
		})
	}
}

func TestFindFilteredRosterPagesAndRefusesPartial953(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "fake953")
	for _, badSecond := range []bool{false, true} {
		t.Run(fmt.Sprint(badSecond), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Variables struct {
						Offset int `json:"offset"`
					} `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				items := fmt.Sprintf(`[{"id":"w%d","name":"Worker%d"}]`, req.Variables.Offset, req.Variables.Offset)
				if req.Variables.Offset == 1 && badSecond {
					items = `[]`
				}
				_, _ = fmt.Fprintf(w, `{"data":{"workers":{"total":2,"items":%s}}}`, items)
			}))
			defer server.Close()
			f := cmdutil.NewFactory()
			f.ServerFlag = server.URL
			f.HTTPClient = server.Client()
			got, err := findFilteredTargets(context.Background(), f, "hrn:app:test.com:team", nil)
			if badSecond {
				if err == nil || got != nil {
					t.Fatal("partial roster accepted", got, err)
				}
			} else if err != nil || len(got) != 2 {
				t.Fatal(got, err)
			}
			if calls.Load() != 2 {
				t.Fatal("short first page truncated roster", calls.Load())
			}
		})
	}
}

func TestFindFilteredSelectedAppSources953(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "fake953")
	const appID = "0123456789abcdef0123456789abcdef"
	for _, mode := range []string{"short flag", "id flag", "configured short", "ambient binding"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
			wantApp := "hrn:app:test.com:team"
			if mode == "id flag" || mode == "ambient binding" {
				wantApp = appID
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Variables map[string]any `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Variables["appRef"] != wantApp {
					t.Error("wrong App query", req, err)
					w.WriteHeader(400)
					return
				}
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"data":{"worker":{"id":"w1","name":"Iris","appId":%q,"appUrn":"hrn:app:test.com:team"}}}`, appID)
			}))
			defer server.Close()
			streams, out, _ := output.Test()
			f := cmdutil.NewFactory()
			f.IOStreams, f.JSON, f.ServerFlag, f.HTTPClient = streams, true, server.URL, server.Client()
			switch mode {
			case "short flag":
				f.AppFlag = "test.com:team"
			case "id flag":
				f.AppFlag = appID
			case "configured short":
				dir := filepath.Join(root, "config", "hadron")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("app = \"test.com:team\"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "ambient binding":
				dir := filepath.Join(root, ".hadron-team")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				binding, err := json.Marshal(map[string]string{"appId": appID, "workerId": "w1", "server": server.URL})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), binding, 0600); err != nil {
					t.Fatal(err)
				}
			}
			source := filterSources{procRoot: t.TempDir(), targets: func(ctx context.Context, app string, args []string) ([]filterreport.Target, error) {
				if app != wantApp {
					t.Fatalf("App source %s: got %q, want %q", mode, app, wantApp)
				}
				return findFilteredTargets(ctx, f, app, args)
			}, runner: func() (orcaterm.Runner, bool) {
				return func(context.Context, []string) orcaterm.Result {
					return orcaterm.Result{Stdout: `{"ok":true,"result":{"terminals":[]}}`}
				}, true
			}}
			cmd := newCmdWorkerFindFilteredWithSources(f, source)
			cmd.SetArgs([]string{"urn:worker:test.com:team:iris"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var rows []filteredWorkerDTO
			if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].Worker != "Iris" || rows[0].Classification != "UNREADABLE" || rows[0].Note != "no_terminal" || calls.Load() != 1 {
				t.Fatal("selected App did not yield the named member", rows, err, calls.Load())
			}
		})
	}
}
