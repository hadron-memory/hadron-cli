package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1071: `automation ls|approval|approve|decline`. The guards that matter are
// "zero or exactly one call" — nothing is approved or declined before the
// person has seen the projection and said yes, the hash sent is the hash shown,
// and a refusal is never retried — so every test asserts per-operation counts.

const autoApp = "hrn:app:acme.com:ops"

type autoReply struct {
	status int // 0 = 200
	body   string
}

// autoServer answers by operation name, counts calls, and records the
// variables of every call. A reply func sees the call's variables, so a paged
// list can answer by cursor.
type autoServer struct {
	mu    sync.Mutex
	calls map[string]int
	vars  map[string][]map[string]any
	URL   string
}

func (s *autoServer) count(op string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[op]
}

func (s *autoServer) lastVars(op string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.vars[op]
	if len(v) == 0 {
		return nil
	}
	return v[len(v)-1]
}

func newAutoServer(t *testing.T, replies map[string]func(vars map[string]any) autoReply) *autoServer {
	t.Helper()
	s := &autoServer{calls: map[string]int{}, vars: map[string][]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.calls[body.OperationName]++
		s.vars[body.OperationName] = append(s.vars[body.OperationName], body.Variables)
		s.mu.Unlock()
		fn, ok := replies[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(memberGQLError("unexpected operation", "INTERNAL_SERVER_ERROR")))
			return
		}
		rep := fn(body.Variables)
		w.Header().Set("Content-Type", "application/json")
		if rep.status != 0 {
			w.WriteHeader(rep.status)
		}
		_, _ = w.Write([]byte(rep.body))
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

func fixed(body string) func(map[string]any) autoReply {
	return func(map[string]any) autoReply { return autoReply{body: body} }
}

func autoRow(kind, ref, name, state, headline string) string {
	return fmt.Sprintf(`{"triggerKind":%q,"triggerRef":%q,"name":%q,"state":%q,"headline":%q}`, kind, ref, name, state, headline)
}

func autoPage(rows []string, next string, omitted bool) string {
	cur := "null"
	if next != "" {
		cur = fmt.Sprintf("%q", next)
	}
	om := `[]`
	if omitted {
		om = `[{"triggerKind":"MANUAL","reason":"approved from the run that needs it"}]`
	}
	return fmt.Sprintf(`{"data":{"automationApprovals":{"rows":[%s],"nextCursor":%s,"omitted":%s}}}`, strings.Join(rows, ","), cur, om)
}

func autoDetail(state, hash, name string) string {
	return fmt.Sprintf(`{"data":{"automationApproval":{"state":%q,"hash":%q,"summary":{"name":%q,"schedule":"every night at 02:00",
		"reads":["what the App can read in this App"],"writes":["a new node runs:<runId> beside the entry"],
		"tools":["hadron_find_nodes"],"limits":["set by the platform at run start"],"cannotDo":["never a private memory"],
		"flows":[{"name":"main","urn":"hrn:node:acme.com:ops:flows:main","tools":["hadron_find_nodes"],"writes":[]}]}}}}`, state, hash, name)
}

const autoHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func oneRowServer(t *testing.T, state string, extra map[string]func(map[string]any) autoReply) *autoServer {
	t.Helper()
	rep := map[string]func(map[string]any) autoReply{
		"AutomationApprovals":     fixed(autoPage([]string{autoRow("SCHEDULE", "sch1", "nightly", state, "Runs nightly")}, "", true)),
		"AutomationApproval":      fixed(autoDetail(state, autoHash, "nightly")),
		"AutomationApprovalDepth": fixed(strings.Replace(autoDetail(state, autoHash, "nightly"), `"state":`, `"depth":{"nodes":[{"loc":"flows:main"}]},"state":`, 1)),
		"ApproveAutomation":       fixed(`{"data":{"approveAutomation":true}}`),
		"DeclineAutomation":       fixed(`{"data":{"declineAutomation":true}}`),
	}
	for k, v := range extra {
		rep[k] = v
	}
	return newAutoServer(t, rep)
}

// autoActiveApp, when set, is written as the active App of every factory
// runAuto builds, and runAuto then omits its default --app (each factory gets a
// fresh config dir, so the active App cannot be set from outside).
var autoActiveApp string

type autoRun struct {
	out, errOut string
	err         error
	code        int
}

// runAuto executes the command with `answers` as stdin. tty=false is the
// non-interactive branch.
func runAuto(t *testing.T, s *autoServer, tty bool, answers string, args ...string) autoRun {
	t.Helper()
	args = append(append([]string{"automation"}, args...), "--server", s.URL)
	if autoActiveApp == "" && !hasFlag(args, "--app") {
		args = append(args, "--app", autoApp)
	}
	var r autoRun
	if tty {
		f, out, eo := testFactoryTTY(t, answers)
		if autoActiveApp != "" {
			setActiveApp(t, autoActiveApp)
		}
		root := NewRootCmd(f)
		root.SetArgs(args)
		r.err = root.Execute()
		r.out, r.errOut = out.String(), eo.String()
	} else {
		f, out := testFactory(t)
		if autoActiveApp != "" {
			setActiveApp(t, autoActiveApp)
		}
		root := NewRootCmd(f)
		root.SetArgs(args)
		r.err = root.Execute()
		r.out = out.String()
		r.errOut = f.IOStreams.ErrOut.(interface{ String() string }).String()
	}
	r.code = exitCodeFor(r.err)
	return r
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

func TestAutomationLsFollowsPagesAndShowsStates(t *testing.T) {
	s := newAutoServer(t, map[string]func(map[string]any) autoReply{
		"AutomationApprovals": func(v map[string]any) autoReply {
			if v["after"] == nil {
				return autoReply{body: autoPage([]string{
					autoRow("SCHEDULE", "s1", "alpha", "ADOPTED", "Runs nightly"),
					autoRow("WEBHOOK", "w1", "beta", "UNADOPTED", "Starts when called"),
				}, "cur1", true)}
			}
			if v["after"] != "cur1" {
				t.Errorf("second page must pass the cursor it was given: %v", v["after"])
			}
			return autoReply{body: autoPage([]string{autoRow("SCHEDULE", "s2", "gamma", "UNADOPTED", "Runs weekly")}, "", true)}
		},
	})
	r := runAuto(t, s, false, "", "ls")
	if r.err != nil || s.count("AutomationApprovals") != 2 {
		t.Fatalf("want 2 page calls and success: err=%v calls=%d", r.err, s.count("AutomationApprovals"))
	}
	for _, want := range []string{"alpha", "beta", "gamma", "approved", "not approved", "Manual runs are approved from the run that needs them", "approval <name>"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("ls output missing %q:\n%s", want, r.out)
		}
	}
	for _, bad := range []string{"up to date", "unchanged", "CHANGED"} {
		if strings.Contains(r.out, bad) {
			t.Errorf("ls must never present ADOPTED as %q:\n%s", bad, r.out)
		}
	}
	if strings.Index(r.out, "alpha") > strings.Index(r.out, "beta") || strings.Index(r.out, "beta") > strings.Index(r.out, "gamma") {
		t.Errorf("server order must be preserved:\n%s", r.out)
	}
}

func TestAutomationLsJSONEmptyArraysNotNull(t *testing.T) {
	s := newAutoServer(t, map[string]func(map[string]any) autoReply{"AutomationApprovals": fixed(autoPage(nil, "", false))})
	r := runAuto(t, s, false, "", "ls", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatalf("stdout must be one JSON object: %v\n%s", err, r.out)
	}
	if string(got["rows"]) != "[]" || string(got["omitted"]) != "[]" {
		t.Errorf("rows and omitted must be [] not null: %s", r.out)
	}
}

func TestAutomationLsJSONShape(t *testing.T) {
	s := oneRowServer(t, "ADOPTED", nil)
	r := runAuto(t, s, false, "", "ls", "--json")
	var got struct {
		App  string `json:"app"`
		Rows []struct {
			TriggerKind string `json:"triggerKind"`
			TriggerRef  string `json:"triggerRef"`
			Name        string `json:"name"`
			State       string `json:"state"`
			Headline    string `json:"headline"`
		} `json:"rows"`
		Omitted []struct {
			TriggerKind string `json:"triggerKind"`
			Reason      string `json:"reason"`
		} `json:"omitted"`
	}
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatalf("%v\n%s", err, r.out)
	}
	if got.App != autoApp || len(got.Rows) != 1 || got.Rows[0].State != "ADOPTED" || got.Rows[0].TriggerKind != "SCHEDULE" ||
		got.Rows[0].Name != "nightly" || len(got.Omitted) != 1 || got.Omitted[0].TriggerKind != "MANUAL" {
		t.Errorf("unexpected --json: %s", r.out)
	}
}

func TestAutomationSelection(t *testing.T) {
	rows := []string{
		autoRow("SCHEDULE", "s1", "nightly", "ADOPTED", "Runs nightly"),
		autoRow("WEBHOOK", "w1", "nightly", "UNADOPTED", "Starts when called"),
		autoRow("SCHEDULE", "s2", "weekly", "UNADOPTED", "Runs weekly"),
		autoRow("WEBHOOK", "w2", "hook", "UNADOPTED", "Starts on push"),
		autoRow("SCHEDULE", "s3", "twin", "UNADOPTED", "first twin"),
		autoRow("SCHEDULE", "s4", "twin", "UNADOPTED", "second twin"),
	}
	srv := func(t *testing.T) *autoServer {
		return oneRowServer(t, "UNADOPTED", map[string]func(map[string]any) autoReply{
			"AutomationApprovals": fixed(autoPage(rows, "", true)),
		})
	}
	t.Run("qualified name picks the kind", func(t *testing.T) {
		s := srv(t)
		r := runAuto(t, s, false, "", "approval", "webhook/nightly")
		v := s.lastVars("AutomationApproval")
		if r.err != nil || v["triggerKind"] != "WEBHOOK" || v["triggerRef"] != "w1" {
			t.Fatalf("want WEBHOOK/w1: err=%v vars=%v", r.err, v)
		}
	})
	t.Run("bare name unique across kinds", func(t *testing.T) {
		s := srv(t)
		r := runAuto(t, s, false, "", "approval", "weekly")
		v := s.lastVars("AutomationApproval")
		if r.err != nil || v["triggerKind"] != "SCHEDULE" || v["triggerRef"] != "s2" {
			t.Fatalf("want SCHEDULE/s2: err=%v vars=%v", r.err, v)
		}
	})
	t.Run("bare name in two kinds refuses and lists only the matches", func(t *testing.T) {
		s := srv(t)
		r := runAuto(t, s, false, "", "approval", "nightly")
		if r.code != exitcode.Usage || s.count("AutomationApproval") != 0 {
			t.Fatalf("want exit 2 and no detail call: code=%d err=%v", r.code, r.err)
		}
		msg := r.err.Error()
		for _, want := range []string{"schedule/nightly", "webhook/nightly", "Runs nightly", "Starts when called"} {
			if !strings.Contains(msg, want) {
				t.Errorf("ambiguity message missing %q: %s", want, msg)
			}
		}
		for _, bad := range []string{"weekly", "hook ", "s1", "w1", "Runs weekly"} {
			if strings.Contains(msg, bad) {
				t.Errorf("ambiguity message must list only the matches, found %q: %s", bad, msg)
			}
		}
	})
	t.Run("same kind and name refuses", func(t *testing.T) {
		s := srv(t)
		r := runAuto(t, s, false, "", "approve", "schedule/twin", "--yes")
		if r.code != exitcode.Usage || s.count("ApproveAutomation") != 0 || s.count("AutomationApproval") != 0 {
			t.Fatalf("want exit 2 and nothing sent: code=%d err=%v", r.code, r.err)
		}
	})
	t.Run("zero matches names the next step and dumps nothing", func(t *testing.T) {
		s := srv(t)
		r := runAuto(t, s, false, "", "approve", "nope", "--yes")
		if r.code != exitcode.NotFound || s.count("ApproveAutomation") != 0 {
			t.Fatalf("want exit 4 and nothing sent: code=%d err=%v", r.code, r.err)
		}
		msg := r.err.Error()
		if !strings.Contains(msg, `no automation named "nope"`) || !strings.Contains(msg, autoApp) || !strings.Contains(msg, "hadron automation ls") {
			t.Errorf("zero-match message: %s", msg)
		}
		for _, bad := range []string{"nightly", "weekly", "hook"} {
			if strings.Contains(msg, bad) {
				t.Errorf("zero-match must not list other automations (%q): %s", bad, msg)
			}
		}
	})
	t.Run("a name and --run are exclusive, and one is required", func(t *testing.T) {
		s := srv(t)
		r := runAuto(t, s, false, "", "approve", "nightly", "--run", "run1", "--yes")
		if r.code != exitcode.Usage {
			t.Errorf("name plus --run: want exit 2, got %d (%v)", r.code, r.err)
		}
		r = runAuto(t, s, false, "", "approve", "--yes")
		if r.code != exitcode.Usage {
			t.Errorf("neither: want exit 2, got %d (%v)", r.code, r.err)
		}
		if s.count("ApproveAutomation") != 0 || s.count("AutomationApprovals") != 0 {
			t.Errorf("a bad selection must send nothing")
		}
	})
	t.Run("--run names a MANUAL origin without listing", func(t *testing.T) {
		s := srv(t)
		r := runAuto(t, s, false, "", "approve", "--run", "run-123", "--yes")
		v := s.lastVars("AutomationApproval")
		if r.err != nil || v["triggerKind"] != "MANUAL" || v["triggerRef"] != "run-123" || s.count("AutomationApprovals") != 0 {
			t.Fatalf("want MANUAL/run-123 and no list call: err=%v vars=%v", r.err, v)
		}
		if got := s.lastVars("ApproveAutomation"); got["triggerKind"] != "MANUAL" || got["triggerRef"] != "run-123" || got["expectedHash"] != autoHash {
			t.Errorf("approve vars: %v", got)
		}
	})
}

func TestAutomationApproveSendsTheDisplayedHash(t *testing.T) {
	s := oneRowServer(t, "UNADOPTED", nil)
	r := runAuto(t, s, false, "", "approve", "schedule/nightly", "--yes", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	v := s.lastVars("ApproveAutomation")
	if v["expectedHash"] != autoHash || v["triggerKind"] != "SCHEDULE" || v["triggerRef"] != "sch1" || s.count("ApproveAutomation") != 1 {
		t.Fatalf("approve must send the hash it displayed, once: %v", v)
	}
	// --yes skips the prompt, never the display: the summary is on stderr.
	for _, want := range []string{"nightly", "every night at 02:00", "hadron_find_nodes", "never a private memory"} {
		if !strings.Contains(r.errOut, want) {
			t.Errorf("--yes must still display the summary on stderr, missing %q:\n%s", want, r.errOut)
		}
	}
	// stdout is the --json object only.
	var got map[string]any
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatalf("stdout must be exactly one JSON object: %v\n%s", err, r.out)
	}
	if got["status"] != "approved" || got["app"] != autoApp {
		t.Errorf("approve --json: %s", r.out)
	}
	if strings.Contains(r.out, "every night") {
		t.Errorf("the display must not reach stdout: %s", r.out)
	}
}

func TestAutomationApproveStaleHashIsNeverRetried(t *testing.T) {
	s := oneRowServer(t, "UNADOPTED", map[string]func(map[string]any) autoReply{
		"ApproveAutomation": fixed(memberGQLError("Automation changed since preview; preview and re-adopt again.", "AUTOMATION_CHANGED")),
	})
	r := runAuto(t, s, false, "", "approve", "nightly", "--yes")
	if r.code != exitcode.Conflict {
		t.Fatalf("AUTOMATION_CHANGED must exit 5, got %d (%v)", r.code, r.err)
	}
	if s.count("ApproveAutomation") != 1 || s.count("AutomationApproval") != 1 {
		t.Fatalf("no second fetch and no second approve: approve=%d detail=%d", s.count("ApproveAutomation"), s.count("AutomationApproval"))
	}
	if !strings.Contains(r.err.Error(), "hadron automation approval") {
		t.Errorf("the refusal must say to run approval again: %v", r.err)
	}
	if r.out != "" {
		t.Errorf("a refusal writes nothing to stdout: %q", r.out)
	}
}

func TestAutomationApproveConfirmation(t *testing.T) {
	t.Run("non-TTY without --yes sends nothing", func(t *testing.T) {
		s := oneRowServer(t, "UNADOPTED", nil)
		r := runAuto(t, s, false, "", "approve", "nightly")
		if r.code != exitcode.Usage || s.count("ApproveAutomation") != 0 {
			t.Fatalf("want exit 2, zero approve: code=%d calls=%d", r.code, s.count("ApproveAutomation"))
		}
	})
	t.Run("no", func(t *testing.T) {
		s := oneRowServer(t, "UNADOPTED", nil)
		r := runAuto(t, s, true, "n\n", "approve", "nightly")
		if r.code != exitcode.Cancelled || s.count("ApproveAutomation") != 0 {
			t.Fatalf("want exit 6, zero approve: code=%d", r.code)
		}
	})
	t.Run("EOF", func(t *testing.T) {
		s := oneRowServer(t, "UNADOPTED", nil)
		r := runAuto(t, s, true, "", "approve", "nightly")
		if r.code != exitcode.Cancelled || s.count("ApproveAutomation") != 0 {
			t.Fatalf("want exit 6, zero approve: code=%d", r.code)
		}
	})
	t.Run("yes shows the summary before asking", func(t *testing.T) {
		s := oneRowServer(t, "UNADOPTED", nil)
		r := runAuto(t, s, true, "y\n", "approve", "nightly")
		if r.err != nil || s.count("ApproveAutomation") != 1 {
			t.Fatalf("want success, one approve: err=%v", r.err)
		}
		i, j := strings.Index(r.errOut, "every night at 02:00"), strings.Index(r.errOut, "(y/N)")
		if i < 0 || j < 0 || i > j {
			t.Errorf("the summary must precede the prompt on stderr:\n%s", r.errOut)
		}
		if !strings.Contains(r.errOut, "schedule/nightly") || !strings.Contains(r.errOut, autoApp) {
			t.Errorf("the prompt must name the trigger and the App:\n%s", r.errOut)
		}
	})
}

func TestAutomationDeclineSendsNoHash(t *testing.T) {
	s := oneRowServer(t, "ADOPTED", nil)
	r := runAuto(t, s, false, "", "decline", "schedule/nightly", "--yes", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	v := s.lastVars("DeclineAutomation")
	if _, has := v["expectedHash"]; has || v["triggerKind"] != "SCHEDULE" || v["triggerRef"] != "sch1" || len(v) != 2 {
		t.Fatalf("decline carries kind and ref only: %v", v)
	}
	if s.count("AutomationApproval") != 0 {
		t.Errorf("decline must not need the projection (an unreadable automation must stay declinable)")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || got["status"] != "declined" {
		t.Errorf("decline --json: %v %s", err, r.out)
	}
}

func TestAutomationDeclineSaysWhatItDoes(t *testing.T) {
	t.Run("adopted", func(t *testing.T) {
		s := oneRowServer(t, "ADOPTED", nil)
		r := runAuto(t, s, true, "n\n", "decline", "nightly")
		for _, want := range []string{"next run will fail", "approved again", "newer approval"} {
			if !strings.Contains(r.errOut, want) {
				t.Errorf("adopted decline must say %q:\n%s", want, r.errOut)
			}
		}
	})
	t.Run("unadopted", func(t *testing.T) {
		s := oneRowServer(t, "UNADOPTED", nil)
		r := runAuto(t, s, true, "n\n", "decline", "nightly")
		if !strings.Contains(r.errOut, "records your decline") || strings.Contains(r.errOut, "next run will fail") {
			t.Errorf("unadopted decline only records the decision:\n%s", r.errOut)
		}
	})
	t.Run("no, EOF and non-TTY mutate nothing", func(t *testing.T) {
		for name, c := range map[string]struct {
			tty     bool
			answers string
			code    int
		}{"no": {true, "n\n", exitcode.Cancelled}, "eof": {true, "", exitcode.Cancelled}, "non-TTY": {false, "", exitcode.Usage}} {
			s := oneRowServer(t, "ADOPTED", nil)
			r := runAuto(t, s, c.tty, c.answers, "decline", "nightly")
			if r.code != c.code || s.count("DeclineAutomation") != 0 {
				t.Errorf("%s: want exit %d and zero declines, got %d / %d", name, c.code, r.code, s.count("DeclineAutomation"))
			}
		}
	})
	t.Run("MANUAL origin", func(t *testing.T) {
		s := oneRowServer(t, "ADOPTED", nil)
		r := runAuto(t, s, false, "", "decline", "--run", "run-9", "--yes")
		v := s.lastVars("DeclineAutomation")
		if r.err != nil || v["triggerKind"] != "MANUAL" || v["triggerRef"] != "run-9" {
			t.Fatalf("MANUAL decline: err=%v vars=%v", r.err, v)
		}
	})
}

func TestAutomationApprovalDisplay(t *testing.T) {
	t.Run("summary without depth carries no hash", func(t *testing.T) {
		s := oneRowServer(t, "UNADOPTED", nil)
		r := runAuto(t, s, false, "", "approval", "nightly")
		if r.err != nil || s.count("AutomationApprovalDepth") != 0 {
			t.Fatalf("plain approval must not fetch the graph: err=%v", r.err)
		}
		if strings.Contains(r.out, autoHash) || !strings.Contains(r.out, "not approved") || !strings.Contains(r.out, "every night at 02:00") {
			t.Errorf("approval text:\n%s", r.out)
		}
	})
	t.Run("--depth adds the graph and the hash", func(t *testing.T) {
		s := oneRowServer(t, "ADOPTED", nil)
		r := runAuto(t, s, false, "", "approval", "nightly", "--depth")
		if r.err != nil || s.count("AutomationApprovalDepth") != 1 {
			t.Fatalf("--depth must fetch the graph once: err=%v", r.err)
		}
		if !strings.Contains(r.out, autoHash) || !strings.Contains(r.out, "flows:main") {
			t.Errorf("--depth output:\n%s", r.out)
		}
	})
	t.Run("CHANGED is shown, as the server said it", func(t *testing.T) {
		s := oneRowServer(t, "CHANGED", nil)
		r := runAuto(t, s, false, "", "approval", "nightly")
		if !strings.Contains(r.out, "changed since it was approved") {
			t.Errorf("CHANGED state:\n%s", r.out)
		}
	})
	t.Run("--json is the typed projection", func(t *testing.T) {
		s := oneRowServer(t, "ADOPTED", nil)
		r := runAuto(t, s, false, "", "approval", "nightly", "--json")
		var got struct {
			App     string `json:"app"`
			State   string `json:"state"`
			Hash    string `json:"hash"`
			Summary struct {
				Name   string   `json:"name"`
				Reads  []string `json:"reads"`
				Tools  []string `json:"tools"`
				Writes []string `json:"writes"`
				Flows  []struct {
					Name   string   `json:"name"`
					Writes []string `json:"writes"`
				} `json:"flows"`
			} `json:"summary"`
			Depth json.RawMessage `json:"depth"`
		}
		if err := json.Unmarshal([]byte(r.out), &got); err != nil {
			t.Fatalf("%v\n%s", err, r.out)
		}
		if got.State != "ADOPTED" || got.Hash != autoHash || got.Summary.Name != "nightly" || len(got.Summary.Flows) != 1 ||
			string(got.Depth) != "" && string(got.Depth) != "null" {
			t.Errorf("projection: %s", r.out)
		}
		if !strings.Contains(r.out, `"writes": []`) {
			t.Errorf("empty valid arrays must serialize as []: %s", r.out)
		}
	})
}

func TestAutomationServerTextIsTerminalSafeAndBounded(t *testing.T) {
	evil := "evil\x1b[31mred\x1b]0;owned\x07\nsecond line\x00end"
	long := strings.Repeat("x", 5000)
	detail := fmt.Sprintf(`{"data":{"automationApproval":{"state":"UNADOPTED","hash":%q,"summary":{"name":%q,"schedule":null,
		"reads":[%q],"writes":[],"tools":[],"limits":[],"cannotDo":[],"flows":[]}}}}`, autoHash, evil, long)
	s := oneRowServer(t, "UNADOPTED", map[string]func(map[string]any) autoReply{
		"AutomationApproval": fixed(detail),
		"AutomationApprovals": fixed(autoPage([]string{
			autoRow("SCHEDULE", "s1", evil, "UNADOPTED", evil)}, "", true)),
	})
	for _, args := range [][]string{{"approval", "evil"}, {"ls"}, {"approve", "evil"}} {
		r := runAuto(t, s, true, "n\n", args...)
		all := r.out + r.errOut
		for _, bad := range []string{"\x1b", "\x07", "\x00"} {
			if strings.Contains(all, bad) {
				t.Errorf("%v: control character %q reached the terminal:\n%q", args, bad, all)
			}
		}
		if strings.Contains(all, long) {
			t.Errorf("%v: a 5000-char server string must be bounded", args)
		}
	}
	// --json passes the server's strings through unchanged.
	r := runAuto(t, s, false, "", "approval", "evil", "--json")
	var got struct {
		Summary struct {
			Name string `json:"name"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || got.Summary.Name != evil {
		t.Errorf("--json must not alter server text: %v %q", err, got.Summary.Name)
	}
}

func TestAutomationMalformedProjectionIsUnavailable(t *testing.T) {
	cases := map[string]string{
		"empty hash":        strings.Replace(autoDetail("UNADOPTED", autoHash, "nightly"), autoHash, "", 1),
		"unknown state":     autoDetail("MAYBE", autoHash, "nightly"),
		"null reads":        strings.Replace(autoDetail("UNADOPTED", autoHash, "nightly"), `"reads":["what the App can read in this App"]`, `"reads":null`, 1),
		"null summary":      fmt.Sprintf(`{"data":{"automationApproval":{"state":"UNADOPTED","hash":%q,"summary":null}}}`, autoHash),
		"null cannotDo":     strings.Replace(autoDetail("UNADOPTED", autoHash, "nightly"), `"cannotDo":["never a private memory"]`, `"cannotDo":null`, 1),
		"null flows":        strings.Replace(autoDetail("UNADOPTED", autoHash, "nightly"), `"flows":[{"name":"main","urn":"hrn:node:acme.com:ops:flows:main","tools":["hadron_find_nodes"],"writes":[]}]`, `"flows":null`, 1),
		"null flow writes":  strings.Replace(autoDetail("UNADOPTED", autoHash, "nightly"), `"writes":[]}]`, `"writes":null}]`, 1),
		"data null no err":  `{"data":null}`,
		"approval null":     `{"data":{"automationApproval":null}}`,
		"empty object body": `{"data":{}}`,
	}
	for name, body := range cases {
		for _, verb := range []string{"approval", "approve"} {
			t.Run(name+"/"+verb, func(t *testing.T) {
				s := oneRowServer(t, "UNADOPTED", map[string]func(map[string]any) autoReply{"AutomationApproval": fixed(body)})
				r := runAuto(t, s, false, "", verb, "nightly", "--yes")
				if r.code != exitcode.Unavailable || s.count("ApproveAutomation") != 0 || r.out != "" {
					t.Fatalf("want exit 7, nothing approved, empty stdout: code=%d err=%v out=%q", r.code, r.err, r.out)
				}
			})
		}
	}
	t.Run("list rows", func(t *testing.T) {
		for name, body := range map[string]string{
			"null rows":      `{"data":{"automationApprovals":{"rows":null,"nextCursor":null,"omitted":[]}}}`,
			"null omitted":   `{"data":{"automationApprovals":{"rows":[],"nextCursor":null,"omitted":null}}}`,
			"CHANGED in row": autoPage([]string{autoRow("SCHEDULE", "s1", "n", "CHANGED", "h")}, "", true),
			"unknown kind":   autoPage([]string{autoRow("CRON", "s1", "n", "ADOPTED", "h")}, "", true),
			"empty ref":      autoPage([]string{autoRow("SCHEDULE", "", "n", "ADOPTED", "h")}, "", true),
			"empty name":     autoPage([]string{autoRow("SCHEDULE", "s1", "", "ADOPTED", "h")}, "", true),
		} {
			s := newAutoServer(t, map[string]func(map[string]any) autoReply{"AutomationApprovals": fixed(body)})
			r := runAuto(t, s, false, "", "ls", "--json")
			if r.code != exitcode.Unavailable || r.out != "" {
				t.Errorf("%s: want exit 7 and no stdout, got %d %q", name, r.code, r.out)
			}
		}
	})
	t.Run("a repeating cursor stops instead of looping", func(t *testing.T) {
		s := newAutoServer(t, map[string]func(map[string]any) autoReply{
			"AutomationApprovals": fixed(autoPage([]string{autoRow("SCHEDULE", "s1", "n", "ADOPTED", "h")}, "same", true)),
		})
		r := runAuto(t, s, false, "", "ls")
		if r.code != exitcode.Unavailable || s.count("AutomationApprovals") > 3 {
			t.Errorf("a cursor that repeats is a server fault: code=%d calls=%d", r.code, s.count("AutomationApprovals"))
		}
	})
}

func TestAutomationOldServerExitsUnavailableBeforeAnyPrompt(t *testing.T) {
	skew := autoReply{status: 400, body: `{"errors":[{"message":"Cannot query field \"automationApprovals\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`}
	skewFn := func(map[string]any) autoReply { return skew }
	for _, args := range [][]string{{"ls"}, {"approval", "nightly"}, {"approve", "nightly"}, {"decline", "nightly"}, {"approve", "--run", "r1"}, {"decline", "--run", "r1"}, {"approval", "--run", "r1"}} {
		s := newAutoServer(t, map[string]func(map[string]any) autoReply{
			"AutomationApprovals": skewFn, "AutomationApproval": skewFn, "AutomationApprovalDepth": skewFn,
			"ApproveAutomation": skewFn, "DeclineAutomation": skewFn,
		})
		r := runAuto(t, s, true, "y\n", args...)
		if r.code != exitcode.Unavailable || !strings.Contains(r.err.Error(), "this server does not support automation approval") {
			t.Errorf("%v: want exit 7 with the skew sentence, got %d: %v", args, r.code, r.err)
		}
		if strings.Contains(r.errOut, "(y/N)") || s.count("ApproveAutomation")+s.count("DeclineAutomation") > 1 {
			t.Errorf("%v: a skewed server must never reach a prompt:\n%s", args, r.errOut)
		}
	}
}

func TestAutomationRefusalExitCodes(t *testing.T) {
	cases := []struct {
		name, body string
		code       int
	}{
		{"forbidden", memberGQLError("Only the run-as user may preview and re-adopt this automation", "FORBIDDEN"), exitcode.Forbidden},
		{"caller cannot read", memberGQLError("You cannot read a memory this automation uses", "FORBIDDEN"), exitcode.Forbidden},
		{"not found", memberGQLError("Automation trigger not found.", "NOT_FOUND"), exitcode.NotFound},
		{"changed", memberGQLError("Automation changed since preview", "AUTOMATION_CHANGED"), exitcode.Conflict},
		{"unadopted", memberGQLError("Automation is not adopted", "AUTOMATION_UNADOPTED"), exitcode.Conflict},
		{"bad input", memberGQLError("bad", "BAD_USER_INPUT"), exitcode.Usage},
	}
	for _, c := range cases {
		for _, op := range []string{"approve", "decline"} {
			t.Run(c.name+"/"+op, func(t *testing.T) {
				s := oneRowServer(t, "UNADOPTED", map[string]func(map[string]any) autoReply{
					"ApproveAutomation": fixed(c.body), "DeclineAutomation": fixed(c.body)})
				r := runAuto(t, s, false, "", op, "nightly", "--yes")
				if r.code != c.code || r.out != "" {
					t.Fatalf("want exit %d, empty stdout: got %d err=%v out=%q", c.code, r.code, r.err, r.out)
				}
				if got := s.count("ApproveAutomation") + s.count("DeclineAutomation"); got != 1 {
					t.Errorf("a refusal is never retried: %d mutation calls", got)
				}
			})
		}
	}
	t.Run("a refusal on the detail read stops before the prompt", func(t *testing.T) {
		s := oneRowServer(t, "UNADOPTED", map[string]func(map[string]any) autoReply{
			"AutomationApproval": fixed(memberGQLError("You cannot read a memory this automation uses", "FORBIDDEN"))})
		r := runAuto(t, s, true, "y\n", "approve", "nightly")
		if r.code != exitcode.Forbidden || s.count("ApproveAutomation") != 0 || strings.Contains(r.errOut, "(y/N)") {
			t.Fatalf("want exit 8, no prompt, no approve: %d\n%s", r.code, r.errOut)
		}
	})
}

func TestAutomationUnconfirmedMutationIsUnavailable(t *testing.T) {
	for _, op := range []string{"approve", "decline"} {
		t.Run(op+" false", func(t *testing.T) {
			s := oneRowServer(t, "UNADOPTED", map[string]func(map[string]any) autoReply{
				"ApproveAutomation": fixed(`{"data":{"approveAutomation":false}}`),
				"DeclineAutomation": fixed(`{"data":{"declineAutomation":false}}`)})
			r := runAuto(t, s, false, "", op, "nightly", "--yes")
			if r.code != exitcode.Unavailable || r.out != "" {
				t.Fatalf("a false answer is not a success: code=%d out=%q", r.code, r.out)
			}
		})
		t.Run(op+" true beside an error is unknown", func(t *testing.T) {
			body := `{"data":{"approveAutomation":true,"declineAutomation":true},"errors":[{"message":"x","extensions":{"code":"FORBIDDEN"}}]}`
			s := oneRowServer(t, "UNADOPTED", map[string]func(map[string]any) autoReply{
				"ApproveAutomation": fixed(body), "DeclineAutomation": fixed(body)})
			r := runAuto(t, s, false, "", op, "nightly", "--yes")
			if r.code != exitcode.Unavailable || !strings.Contains(r.err.Error(), "outcome unknown") || r.out != "" {
				t.Fatalf("partial data is outcome unknown: code=%d err=%v", r.code, r.err)
			}
		})
	}
}

func TestAutomationAppIsResolvedLikeOtherAppCommands(t *testing.T) {
	t.Run("--app is canonicalized and reaches the server", func(t *testing.T) {
		s := oneRowServer(t, "UNADOPTED", nil)
		r := runAuto(t, s, false, "", "ls", "--app", "acme.com:ops")
		if r.err != nil || s.lastVars("AutomationApprovals")["appRef"] != autoApp {
			t.Fatalf("want canonical %s: err=%v vars=%v", autoApp, r.err, s.lastVars("AutomationApprovals"))
		}
	})
	t.Run("a blank --app refuses instead of using the active App", func(t *testing.T) {
		s := oneRowServer(t, "UNADOPTED", nil)
		autoActiveApp = memberActiveApp
		t.Cleanup(func() { autoActiveApp = "" })
		for _, verb := range []string{"ls", "approval nightly", "approve nightly", "decline nightly"} {
			r := runAuto(t, s, false, "", append(strings.Fields(verb), "--app", "", "--yes")...)
			if r.code != exitcode.Usage {
				t.Errorf("%s: want exit 2, got %d (%v)", verb, r.code, r.err)
			}
		}
		if s.count("AutomationApprovals")+s.count("ApproveAutomation")+s.count("DeclineAutomation") != 0 {
			t.Errorf("a blank --app must send nothing")
		}
	})
	t.Run("no App at all refuses", func(t *testing.T) {
		s := oneRowServer(t, "UNADOPTED", nil)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"automation", "ls", "--server", s.URL})
		if err := root.Execute(); exitCodeFor(err) != exitcode.Usage || s.count("AutomationApprovals") != 0 {
			t.Fatalf("want exit 2 and nothing sent, got %v", err)
		}
	})
	t.Run("the active App is used and named", func(t *testing.T) {
		s := oneRowServer(t, "UNADOPTED", nil)
		autoActiveApp = memberActiveApp
		t.Cleanup(func() { autoActiveApp = "" })
		r := runAuto(t, s, true, "n\n", "decline", "nightly")
		if s.lastVars("AutomationApprovals")["appRef"] != memberActiveApp || !strings.Contains(r.errOut, memberActiveApp) {
			t.Errorf("active App must be used and named in the prompt: %v\n%s", s.lastVars("AutomationApprovals"), r.errOut)
		}
	})
}

func TestAutomationLegacyOperationsStayUnbound(t *testing.T) {
	b, err := os.ReadFile("../api/unbound-ops.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"previewAutomation", "reAdoptAutomation"} {
		if !strings.Contains(string(b), op+" ") && !strings.Contains(string(b), op+"\n") {
			t.Errorf("%s has no CLI call site and must stay in unbound-ops.txt", op)
		}
	}
	for _, op := range []string{"automationApprovals", "automationApproval", "approveAutomation", "declineAutomation"} {
		if strings.Contains(string(b), "\n"+op+" ") || strings.Contains(string(b), "\n"+op+"\n") {
			t.Errorf("%s is bound by this slice and must not be listed unbound", op)
		}
	}
}
