package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAppLeaveRejoinIsConditional1639(t *testing.T) {
	for _, args := range [][]string{{"app", "leave", "--help"}, {"agentic-usage"}, {"app", "leave", joinApp1625, "--yes"}} {
		_, r := run1625(nil).exec(t, args...)
		if r.err != nil {
			t.Fatal(r.err)
		}
		if args[0] == "agentic-usage" {
			start := strings.Index(r.out, "- `app join <app-ref>`")
			if start < 0 {
				t.Fatal("missing membership section")
			}
			r.out = r.out[start:]
			if end := strings.Index(r.out[2:], "\n- `"); end >= 0 {
				r.out = r.out[:end+2]
			}
		}
		for _, want := range []string{"personal memory", "kept", "self-join policy", "capacity"} {
			if !strings.Contains(r.out, want) {
				t.Errorf("%v lacks %q", args, want)
			}
		}
		if strings.Contains(r.out, "Reversible: join again") {
			t.Errorf("%v promises rejoin", args)
		}
	}
	srv, calls, _ := countingGraphQL(t, responses1625())
	f, out, errOut := testFactoryTTY(t, "n\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "leave", joinApp1625, "--server", srv.URL})
	err := root.Execute()
	if err == nil || calls("LeaveApp") != 0 {
		t.Fatalf("decline: err=%v calls=%d", err, calls("LeaveApp"))
	}
	prompt := out.String() + errOut.String()
	for _, want := range []string{"personal memory", "kept", "self-join policy", "capacity"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q: %s", want, prompt)
		}
	}
}

func TestAppJoinMalformedAnswers1639(t *testing.T) {
	for _, body := range []string{`{"data":{"joinApp":null}}`, `{"data":{"joinApp":{"appMember":null}}}`, `{"data":{"joinApp":{"appMember":{"user":null}}}}`} {
		if !json.Valid([]byte(body)) {
			t.Fatal("invalid fixture", body)
		}
		for _, jsonMode := range []bool{false, true} {
			resp := responses1625()
			resp["JoinApp"] = body
			args := []string{"app", "join", joinApp1625}
			if jsonMode {
				args = append(args, "--json")
			}
			calls, r := run1625(resp).exec(t, args...)
			if r.err == nil || exitCodeFor(r.err) != 7 || !strings.Contains(r.err.Error(), "outcome unknown") || !strings.Contains(r.err.Error(), "app member list") {
				t.Fatalf("%s json=%t: err=%v", body, jsonMode, r.err)
			}
			if len(writes1592(calls, "JoinApp")) != 1 {
				t.Fatalf("retry: %+v", *calls)
			}
			if strings.Contains(r.out, "✓") || strings.Contains(r.out, `"appId"`) || (!jsonMode && r.out != "") {
				t.Fatalf("success output: %q", r.out)
			}
		}
	}
}
