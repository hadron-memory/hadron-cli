package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1202: for an automation whose entry node no longer resolves, the server
// (#2503 staleEntryApproval) answers hash:"" with ONE user-meaningful limits
// sentence. The CLI admits that exact structural shape and shows it; it never
// matches the sentence's text. approve exits 5 and sends nothing.
const staleLimit = "The entry node hrn:node:acme.com:ops:flows:gone this automation was set up with no longer resolves (renamed, moved, deleted or unreadable). Set the trigger to the entry node's current URN, then review and approve again."

type staleOpt func(m map[string]any)

// staleBody builds the stale answer; each opt may break one structural rule.
func staleBody(withDepth bool, opts ...staleOpt) string {
	sum := map[string]any{
		"name": "hrn:node:acme.com:ops:flows:gone", "schedule": nil,
		"reads": []string{}, "writes": []string{}, "tools": []string{}, "cannotDo": []string{},
		"flows": []any{}, "limits": []string{staleLimit},
	}
	a := map[string]any{"state": "UNADOPTED", "hash": "", "summary": sum}
	if withDepth {
		a["depth"] = map[string]any{}
	}
	for _, o := range opts {
		o(a)
	}
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"automationApproval": a}})
	return string(b)
}

func staleServer(t *testing.T, opts ...staleOpt) *autoServer {
	t.Helper()
	return oneRowServer(t, "UNADOPTED", map[string]func(map[string]any) autoReply{
		"AutomationApproval":      fixed(staleBody(false, opts...)),
		"AutomationApprovalDepth": fixed(staleBody(true, opts...)),
	})
}

func TestStaleEntryApprovalIsShown1202(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"human", []string{"approval", "nightly"}},
		{"human depth", []string{"approval", "nightly", "--depth"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := staleServer(t)
			r := runAuto(t, s, false, "", tc.args...)
			if r.err != nil || r.code != 0 {
				t.Fatalf("a stale entry is a successful read: code=%d err=%v", r.code, r.err)
			}
			if !strings.Contains(r.out, "not approved") || !strings.Contains(r.out, staleLimit) {
				t.Errorf("state and the limits sentence must be shown:\n%s", r.out)
			}
		})
	}
	for _, depth := range []bool{false, true} {
		t.Run(fmt.Sprintf("json depth=%v", depth), func(t *testing.T) {
			s := staleServer(t)
			args := []string{"approval", "nightly", "--json"}
			if depth {
				args = append(args, "--depth")
			}
			r := runAuto(t, s, false, "", args...)
			var got struct {
				State   string `json:"state"`
				Hash    string `json:"hash"`
				Summary struct {
					Limits []string `json:"limits"`
					Reads  []string `json:"reads"`
				} `json:"summary"`
				Depth json.RawMessage `json:"depth"`
			}
			if err := json.Unmarshal([]byte(r.out), &got); err != nil || r.code != 0 {
				t.Fatalf("code=%d err=%v out=%s", r.code, err, r.out)
			}
			if got.State != "UNADOPTED" || got.Hash != "" || len(got.Summary.Limits) != 1 || got.Summary.Limits[0] != staleLimit || got.Summary.Reads == nil {
				t.Errorf("projection: %s", r.out)
			}
			if depth && strings.TrimSpace(string(got.Depth)) != "{}" {
				t.Errorf("--depth must carry {}: %s", r.out)
			}
		})
	}
}

func TestStaleEntryApproveRefusesWithoutWriting1202(t *testing.T) {
	cases := map[string]struct {
		tty     bool
		answers string
		args    []string
	}{
		"--yes":                {false, "", []string{"approve", "nightly", "--yes"}},
		"non-interactive":      {false, "", []string{"approve", "nightly"}},
		"interactive says yes": {true, "y\n", []string{"approve", "nightly"}},
		"--yes --json":         {false, "", []string{"approve", "nightly", "--yes", "--json"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := staleServer(t)
			r := runAuto(t, s, c.tty, c.answers, c.args...)
			if r.code != exitcode.Conflict || s.count("ApproveAutomation") != 0 {
				t.Fatalf("want exit 5 and zero approvals: code=%d err=%v approvals=%d", r.code, r.err, s.count("ApproveAutomation"))
			}
			if r.out != "" {
				t.Errorf("stdout must stay empty on a refusal: %q", r.out)
			}
			shown := r.errOut
			if r.err != nil {
				shown += r.err.Error()
			}
			if !strings.Contains(shown, staleLimit) || !strings.Contains(shown, "nothing was approved") ||
				!strings.Contains(shown, "hadron automation approval") {
				t.Errorf("summary and the next step must be shown:\n%s", shown)
			}
			if strings.Contains(r.errOut, "Approve ") && strings.Contains(r.errOut, "as shown above?") {
				t.Errorf("no prompt for an automation that cannot be approved:\n%s", r.errOut)
			}
		})
	}
}

func TestStaleEntryDeclineUnchanged1202(t *testing.T) {
	s := staleServer(t)
	r := runAuto(t, s, false, "", "decline", "nightly", "--yes")
	if r.err != nil || s.count("DeclineAutomation") != 1 {
		t.Fatalf("decline must still work on the stale row: err=%v declines=%d", r.err, s.count("DeclineAutomation"))
	}
}

func TestStaleEntryNearMissesStayMalformed1202(t *testing.T) {
	set := func(k string, v any) staleOpt {
		return func(a map[string]any) { a["summary"].(map[string]any)[k] = v }
	}
	cases := map[string]staleOpt{
		"state ADOPTED":    func(a map[string]any) { a["state"] = "ADOPTED" },
		"state CHANGED":    func(a map[string]any) { a["state"] = "CHANGED" },
		"one flow":         set("flows", []any{map[string]any{"name": "f", "urn": "u", "tools": []string{}, "writes": []string{}}}),
		"non-empty reads":  set("reads", []string{"x"}),
		"non-empty writes": set("writes", []string{"x"}),
		"non-empty tools":  set("tools", []string{"x"}),
		"non-empty cannot": set("cannotDo", []string{"x"}),
		"null reads":       set("reads", nil),
		"null cannotDo":    set("cannotDo", nil),
		"null flows":       set("flows", nil),
		"schedule present": set("schedule", "every night"),
		"zero limits":      set("limits", []string{}),
		"two limits":       set("limits", []string{"a", "b"}),
		"null limits":      set("limits", nil),
		"empty limit":      set("limits", []string{""}),
		"empty name":       set("name", ""),
	}
	for name, opt := range cases {
		for _, verb := range []string{"approval", "approve"} {
			t.Run(name+"/"+verb, func(t *testing.T) {
				s := staleServer(t, opt)
				args := []string{verb, "nightly"}
				if verb == "approve" {
					args = append(args, "--yes")
				}
				r := runAuto(t, s, false, "", args...)
				if r.code != exitcode.Unavailable || s.count("ApproveAutomation") != 0 || r.out != "" {
					t.Fatalf("want exit 7, zero writes, empty stdout: code=%d err=%v out=%q", r.code, r.err, r.out)
				}
			})
		}
	}
	for _, depthBody := range []string{`{"nodes":[]}`, `[]`, `null`, `0`} {
		t.Run("depth "+depthBody, func(t *testing.T) {
			body := strings.Replace(staleBody(false), `"state":`, `"depth":`+depthBody+`,"state":`, 1)
			s := oneRowServer(t, "UNADOPTED", map[string]func(map[string]any) autoReply{"AutomationApprovalDepth": fixed(body)})
			r := runAuto(t, s, false, "", "approval", "nightly", "--depth")
			if r.code != exitcode.Unavailable || r.out != "" {
				t.Fatalf("a stale answer with depth %s is malformed: code=%d out=%q", depthBody, r.code, r.out)
			}
		})
	}
}

func TestStaleEntrySentenceIsTerminalSafe1202(t *testing.T) {
	evil := "gone\x1b[31mred\x1b]0;owned\x07\x00end"
	s := staleServer(t, func(a map[string]any) { a["summary"].(map[string]any)["limits"] = []string{evil} })
	for _, args := range [][]string{{"approval", "nightly"}, {"approve", "nightly", "--yes"}} {
		r := runAuto(t, s, false, "", args...)
		all := r.out + r.errOut
		if r.err != nil {
			all += r.err.Error()
		}
		for _, bad := range []string{"\x1b", "\x07", "\x00"} {
			if strings.Contains(all, bad) {
				t.Errorf("%v: control character %q reached the terminal: %q", args, bad, all)
			}
		}
	}
}

// Blind P2 (Beckett): a stale answer whose sentence is over the display bound
// must still exit 5 with the repair step — there is nothing to approve, so
// "the whole summary must be on screen" serves no purpose.
func TestStaleEntryOversizedSentenceStillExits5(t *testing.T) {
	long := staleLimit + strings.Repeat(" x", 3000)
	s := staleServer(t, func(a map[string]any) { a["summary"].(map[string]any)["limits"] = []string{long} })
	for _, args := range [][]string{{"approve", "nightly", "--yes"}, {"approve", "nightly"}} {
		r := runAuto(t, s, true, "y\n", args...)
		shown := r.errOut
		if r.err != nil {
			shown += r.err.Error()
		}
		if r.code != exitcode.Conflict || s.count("ApproveAutomation") != 0 || r.out != "" {
			t.Fatalf("%v: want exit 5, zero approvals, empty stdout: code=%d err=%v", args, r.code, r.err)
		}
		if !strings.Contains(shown, "nothing was approved") || !strings.Contains(shown, "hadron automation approval") ||
			strings.Contains(shown, "too large to show") {
			t.Errorf("%v: want the repair step, not the too-large refusal:\n%s", args, shown)
		}
		if strings.Contains(r.errOut, "as shown above?") {
			t.Errorf("%v: no prompt:\n%s", args, r.errOut)
		}
	}
}

// Diego's LOCAL probe: a valid entry URN longer than the name display bound
// also marks the display incomplete; it must still exit 5, not 7.
func TestStaleEntryLongNameStillExits5(t *testing.T) {
	longName := "hrn:node:acme.com:ops:" + strings.Repeat("deep:", 60) + "gone"
	s := staleServer(t, func(a map[string]any) { a["summary"].(map[string]any)["name"] = longName })
	r := runAuto(t, s, false, "", "approve", "nightly", "--yes")
	shown := r.errOut
	if r.err != nil {
		shown += r.err.Error()
	}
	if r.code != exitcode.Conflict || s.count("ApproveAutomation") != 0 || !strings.Contains(shown, "hadron automation approval") ||
		strings.Contains(shown, "too large to show") {
		t.Fatalf("a long stale name must exit 5 with the repair step: code=%d\n%s", r.code, shown)
	}
}
