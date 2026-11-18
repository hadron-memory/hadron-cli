package team

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	if err := os.WriteFile(filepath.Join(wt, ".hadron-team", "hadron-team-session.json"), []byte(`{"workerId":"w1"}`), 0600); err != nil {
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
					b, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminal": map[string]any{"source": "screen", "tail": []string{"ⓘ This content can’t be shown", "  Apply for Daybreak: https://openai.com/form/", "› "}, "draft": secret}}})
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
