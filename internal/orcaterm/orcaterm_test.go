package orcaterm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// --- #906 item 1: the per-call bound (hadron-server#1940) -------------------

func TestCallTimeoutInvariants(t *testing.T) {
	// router.test.ts 'bounds every child call at 90s — ≥ busyWaitMs + margin,
	// never the old 300s'. Here the long call is send's --wait-submit.
	if CallTimeout != 90*time.Second || MinCallTimeout != 75*time.Second {
		t.Fatalf("bound %s floor %s, want 90s and 75s", CallTimeout, MinCallTimeout)
	}
	if CallTimeout < MinCallTimeout || time.Duration(SubmitWait)*time.Second > CallTimeout-15*time.Second {
		t.Fatalf("--wait-submit %ds leaves the %s bound no margin", SubmitWait, CallTimeout)
	}
}

func script(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell script")
	}
	p := filepath.Join(t.TempDir(), "orca")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExecFailClosed(t *testing.T) {
	ctx := context.Background()
	t.Run("a nonexistent binary reads as code 1 with EMPTY stdout (#1940 cycle 2)", func(t *testing.T) {
		res := Exec("/nonexistent/binary/that/does/not/exist", CallTimeout)(ctx, []string{"terminal", "list"})
		if res.Code != 1 || res.Stdout != "" || !strings.Contains(res.Stderr, "call failed") {
			t.Fatalf("got %+v", res)
		}
	})
	t.Run("a SIGTERM-ignoring child that printed success JSON is killed and reads as code 1 with EMPTY stdout (#1940 cycle 1)", func(t *testing.T) {
		bin := script(t, `trap '' TERM; printf '{"ok":true}'; sleep 5; exit 0`)
		start := time.Now()
		res := Exec(bin, 150*time.Millisecond)(ctx, []string{"terminal", "list"})
		if res.Code != 1 || res.Stdout != "" || !strings.Contains(res.Stderr, "exceeded") {
			t.Fatalf("got %+v", res)
		}
		if time.Since(start) > 3*time.Second {
			t.Fatalf("the kill was not hard: took %s", time.Since(start))
		}
	})
	t.Run("a child killed by a signal after printing success JSON reads as code 1 with EMPTY stdout", func(t *testing.T) {
		bin := script(t, `printf '{"ok":true}'; kill -9 $$`)
		res := Exec(bin, CallTimeout)(ctx, []string{"terminal", "list"})
		if res.Code != 1 || res.Stdout != "" {
			t.Fatalf("got %+v", res)
		}
	})
	t.Run("an ordinary non-zero exit keeps its output: Orca's error envelope is the verdict", func(t *testing.T) {
		bin := script(t, `printf '{"ok":false,"error":{"code":"x"}}'; exit 3`)
		res := Exec(bin, CallTimeout)(ctx, []string{"terminal", "list"})
		if res.Code != 3 || res.Stdout == "" {
			t.Fatalf("got %+v", res)
		}
	})
	t.Run("a normally exiting child is untouched (control)", func(t *testing.T) {
		bin := script(t, `printf fine`)
		res := Exec(bin, CallTimeout)(ctx, []string{"terminal", "list"})
		if res.Code != 0 || res.Stdout != "fine" {
			t.Fatalf("got %+v", res)
		}
	})
}

// --- the call allowlist ------------------------------------------------------

func TestGuardAllowsOnlyTheFourDeliveryCalls(t *testing.T) {
	var ran [][]string
	run := Guard(func(_ context.Context, argv []string) Result {
		ran = append(ran, argv)
		return Result{Stdout: `{"ok":true}`}
	})
	for _, argv := range [][]string{
		{"orchestration", "send", "--to", "x"}, {"terminal", "create"}, {"terminal", "close"},
		{"terminal", "rename"}, {"terminal"}, {},
	} {
		if res := run(context.Background(), argv); res.Code != 1 || res.Stdout != "" {
			t.Errorf("%v: got %+v, want the refused fail-closed shape", argv, res)
		}
	}
	if len(ran) != 0 {
		t.Fatalf("refused calls reached the runner: %v", ran)
	}
	for _, argv := range [][]string{{"terminal", "list"}, {"terminal", "read"}, {"terminal", "wait"}, {"terminal", "send"}} {
		if res := run(context.Background(), argv); res.Code != 0 {
			t.Errorf("%v refused", argv)
		}
	}
}

func TestNoOrchestrationAnywhereInThePackage(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, `"orchestration"`) {
				t.Errorf("%s calls orchestration: %s", f, l)
			}
		}
	}
}

// --- send ---------------------------------------------------------------------

type recorder struct {
	calls   [][]string
	replies []string
}

func (r *recorder) first(t *testing.T) []string {
	t.Helper()
	if len(r.calls) == 0 {
		t.Fatal("no orca call was made")
	}
	return r.calls[0]
}

func (r *recorder) run(_ context.Context, argv []string) Result {
	r.calls = append(r.calls, argv)
	out := `{"ok":true}`
	if len(r.replies) > 0 {
		out, r.replies = r.replies[0], r.replies[1:]
	}
	return Result{Stdout: out}
}

const accepted = `{"ok":true,"result":{"send":{"accepted":true,"prompt":{"requestId":"req-1","stages":["input_accepted","turn_started"]}}}}`

func TestSendShape(t *testing.T) {
	t.Run("interrupt only when asked", func(t *testing.T) {
		for _, interrupt := range []bool{false, true} {
			r := &recorder{replies: []string{accepted}}
			NewClient(r.run).Send(context.Background(), "term_1", "[hadron chat #1] x", SendOptions{Interrupt: interrupt})
			if got := slices.Contains(r.first(t), "--interrupt"); got != interrupt {
				t.Errorf("Interrupt=%v sent --interrupt=%v: %v", interrupt, got, r.calls[0])
			}
		}
	})
	t.Run("an Enter-only send types no text", func(t *testing.T) {
		r := &recorder{replies: []string{accepted}}
		NewClient(r.run).Send(context.Background(), "term_1", "[hadron chat #1] x", SendOptions{EnterOnly: true})
		if argv := r.first(t); slices.Contains(argv, "--text") || !slices.Contains(argv, "--enter") {
			t.Fatalf("argv %v", r.calls[0])
		}
	})
	t.Run("a NoEnter send types without submitting", func(t *testing.T) {
		r := &recorder{replies: []string{accepted}}
		NewClient(r.run).Send(context.Background(), "term_1", "\x15", SendOptions{NoEnter: true})
		if argv := r.first(t); slices.Contains(argv, "--enter") || !slices.Contains(argv, "--text") {
			t.Fatalf("argv %v", argv)
		}
	})
	t.Run("turn_started is the strong evidence; input_accepted alone is not", func(t *testing.T) {
		r := &recorder{replies: []string{accepted}}
		if rc := NewClient(r.run).Send(context.Background(), "t", "x", SendOptions{}); !rc.Accepted || !rc.TurnStarted {
			t.Fatalf("got %+v", rc)
		}
		r = &recorder{replies: []string{`{"ok":true,"result":{"send":{"accepted":true,"prompt":{"stages":["input_accepted"]}}}}`}}
		if rc := NewClient(r.run).Send(context.Background(), "t", "x", SendOptions{}); !rc.Accepted || rc.TurnStarted {
			t.Fatalf("got %+v", rc)
		}
	})
	t.Run("one retry with the durable request id, only after a non-accepted send that offers one", func(t *testing.T) {
		r := &recorder{replies: []string{`{"ok":false,"error":{"code":"transport","requestId":"req-9"}}`, accepted}}
		rc := NewClient(r.run).Send(context.Background(), "t", "x", SendOptions{})
		if !rc.Accepted || len(r.calls) != 2 || !slices.Contains(r.calls[1], "req-9") {
			t.Fatalf("got %+v after %v", rc, r.calls)
		}
		r = &recorder{replies: []string{`{"ok":false,"error":{"code":"stale"}}`}}
		if rc := NewClient(r.run).Send(context.Background(), "t", "x", SendOptions{}); rc.Accepted || len(r.calls) != 1 || rc.ErrCode != "stale" {
			t.Fatalf("no id must mean no retry: %+v after %v", rc, r.calls)
		}
	})
	t.Run("a refusal carries Orca's code, never its message (which may quote the text)", func(t *testing.T) {
		r := &recorder{replies: []string{`{"ok":false,"error":{"code":"rejected","message":"cannot send SECRET-CANARY"}}`}}
		rc := NewClient(r.run).Send(context.Background(), "t", "SECRET-CANARY", SendOptions{})
		b, _ := json.Marshal(rc)
		if strings.Contains(string(b), "CANARY") || rc.ErrCode != "rejected" {
			t.Fatalf("receipt %s", b)
		}
	})
}

func TestReadScreenAndList(t *testing.T) {
	r := &recorder{replies: []string{
		`{"ok":true,"result":{"terminals":[{"handle":"term_1","worktreePath":"/w/a","agentIdentity":"claude","connected":false}]}}`,
		`{"ok":true,"result":{"terminal":{"source":"screen","tail":["a","b"],"draft":"typed"}}}`,
		`{"ok":false,"error":{"code":"terminal_handle_stale"}}`,
	}}
	c := NewClient(r.run)
	terms, err := c.List(context.Background())
	if err != nil || len(terms) != 1 || terms[0].Identity() != "claude" || terms[0].Connected || !terms[0].Writable {
		t.Fatalf("list %+v %v", terms, err)
	}
	if sc := c.ReadScreen(context.Background(), "term_1"); sc == nil || sc.Draft != "typed" || len(sc.Tail) != 2 {
		t.Fatalf("screen %+v", sc)
	}
	if sc := c.ReadScreen(context.Background(), "term_1"); sc != nil {
		t.Fatalf("an error envelope must read as unreadable: %+v", sc)
	}
}

// --- #906 items 2 and 3: the final recap and notice rules ----------------------

func withBefore(screen []string, at int, line string) []string {
	out := append([]string{}, screen[:at]...)
	out = append(out, line)
	return append(out, screen[at:]...)
}

func TestIdleProofRouterPins(t *testing.T) {
	// Named after router.test.ts at hadron-server b8384631: router 'DELIVERS'
	// is Idle here, 'HOLDS … unrecognized chrome' is Unknown, and 'a running
	// turn is drawn' is Busy.
	cases := []struct {
		name   string
		family string
		tail   []string
		want   Liveness
	}{
		{"a completed Codex turn whose ↳ Recap tail trails the Worked-for footer DELIVERS (#1968, Diego)", "codex", codexIdleRecapDiego, Idle},
		{"a Worked-for footer 11 lines above the prompt is INSIDE the deepened window and delivers (#1968, Gene)", "codex", codexIdleRecapGene, Idle},
		{"a recap tail over an in-window Worked-for footer is skipped like trailing banner chrome and delivers (#1968, Xavier)", "codex", codexIdleRecapXavier, Idle},
		{"a folded-URL receipt pushing Worked-for past the old 8-line fold still delivers (#1968, Atilla)", "codex", codexIdleUrlReceipt, Idle},
		{"a deep-indented tail with NO idle marker anywhere on screen still HOLDS (#1968)", "codex", codexUnknownIndentNoMarker, Unknown},
		{"a deep indent with NO recap header is a current status (Andi P2, #1976 review)", "codex", codexUnknownIndentedStatusNoHeader, Unknown},
		{"an unindented current status over an old Worked-for marker holds (Andi control, #1976 review)", "codex", codexUnknownStatusNoRecap, Unknown},
		{"a Claude recap block (header 2, continuation 4) trails its done-marker and DELIVERS (#1977, Yuri)", "claude", claudeIdleRecapYuri, Idle},
		{"4-indent output under a done-marker with NO recap header is unrecognized and HOLDS (#1977 counterexample)", "claude", claudeUnknownCodeblockNoHeader, Unknown},
		{"a content-suppressed turn (notice box, NO Worked-for footer) is completed chrome and DELIVERS (#1983, Riana)", "codex", codexIdleNoticeRiana, Idle},
		{"a suppressed turn with NO output at all — └ (no output) + the notice — DELIVERS (#1983, Chip)", "codex", codexIdleNoticeChip, Idle},
		{"a pinned busy line ABOVE a notice box still wins (#1983)", "codex", withBefore(codexIdleNoticeChip, 7, "  • Working (6s • esc to interrupt)"), Busy},
		{"a newer SAME-INDENT unknown status after a RECAP header is judged, not swallowed — holds (Andi P2, #1985 review)", "codex", []string{
			"  Worked for 1m 4s • 4:04 PM",
			"  ↳ Recap: The task is to read Hadron team chat unfiltered,",
			"            mentions, and mark it read.",
			"  an unrecognized current status at the header indent",
			"› Ask Codex to do anything",
			"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/andi · Static review…",
			"  ? for shortcuts",
		}, Unknown},
		{"a newer SAME-INDENT unknown status after the NOTICE header is judged, not swallowed — holds (Andi P2, #1985 review)", "codex", []string{
			"  ⓘ This content can’t be shown",
			"  We take extra care with some cybersecurity requests.",
			"  an unrecognized current status at the header indent",
			"› Ask Codex to do anything",
			"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/andi · Static review…",
			"  ? for shortcuts",
		}, Unknown},
		{"a BARE Learn-more URL with no notice above is ordinary output and HOLDS (Andi delta P2, #1985 round 2)", "codex", []string{
			"  Worked for 2m 6s • 16:32",
			"  Learn more: https://example.com/documentation",
			"› Ask Codex to do anything",
			"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/andi · Static review…",
			"  ? for shortcuts",
		}, Unknown},
		{"a BARE Apply label with no notice above is ordinary output and HOLDS (Andi delta P2, #1985 round 2)", "codex", []string{
			"  Worked for 2m 6s • 16:32",
			"  Apply for Daybreak: https://openai.com/form/enterprise-trusted-access/",
			"› Ask Codex to do anything",
			"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/andi · Static review…",
			"  ? for shortcuts",
		}, Unknown},
		{"a BARE wrapped notice URL fragment with no notice above HOLDS (Andi delta P2, #1985 round 2)", "codex", []string{
			"  Worked for 2m 6s • 16:32",
			"  openai.com/form/enterprise-trusted-access-for-cyber/",
			"› Ask Codex to do anything",
			"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/andi · Static review…",
			"  ? for shortcuts",
		}, Unknown},
		{"a status line BETWEEN the notice header and its body ends the block — HOLDS (#12876)", "codex", []string{
			"  ⓘ This content can’t be shown",
			"  an unrecognized current status arrived after the header",
			"  Learn more: https://help.openai.com/en/articles/20001326",
			"› Ask Codex to do anything",
			"  GPT-6.1-Sol default · ~/orca/workspaces/hadron-server/andi · Static review…",
			"  ? for shortcuts",
		}, Unknown},
		{"a pinned busy line ABOVE a recap tail still wins (#1968)", "codex", withBefore(codexIdleRecapXavier, 9, "  • Working (6s • esc to interrupt)"), Busy},
		{"a COLUMN-ZERO status line after a recap block ends the block and is judged (Andi round 2, #1976 review)", "codex", withBefore(codexIdleRecapXavier, 9, "a column-zero current status nobody classified"), Unknown},
		{"a COLUMN-ZERO known busy line after a recap block is caught by the sweep (Chip runtime matrix, #1976 review)", "codex", withBefore(codexIdleRecapXavier, 9, "• Working (6s • esc to interrupt)"), Busy},
		{"the shared idle shapes stay idle (claude)", "claude", claudeIdle, Idle},
		{"the shared idle shapes stay idle (codex)", "codex", codexIdle, Idle},
		{"the shared busy shapes stay busy (claude)", "claude", claudeBusyEmpty, Busy},
		{"the shared busy shapes stay busy (codex)", "codex", codexBusyEmpty, Busy},
		{"an unknown family holds", "glm", claudeIdle, Unknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := IdleProof(c.tail, c.family)
			if got != c.want {
				t.Fatalf("IdleProof = %s, want %s", got, c.want)
			}
		})
	}
}

func TestIdleProofReasonNeverCarriesTheScreen(t *testing.T) {
	// The router returned the matched line as its detail; delivery logs the
	// reason, so a reason is a code and never the screen's text.
	for _, tail := range [][]string{
		withBefore(codexIdle, 1, "  • Working (CANARY-LINE • esc to interrupt)"),
		withBefore(codexIdle, 1, "  CANARY-LINE unrecognized"),
		append([]string{"CANARY-LINE"}, codexIdle...),
	} {
		_, reason := IdleProof(tail, "codex")
		if strings.Contains(reason, "CANARY") {
			t.Fatalf("reason %q carries screen text", reason)
		}
	}
}

// --- R2: the composer verdict ----------------------------------------------------

const prefix = "[hadron chat"

func claudeWith(box ...string) []string {
	out := []string{"✻ Cogitated for 1m 3s", rule}
	out = append(out, box...)
	return append(out, rule, "  ⏵⏵ bypass permissions on (shift+tab to cycle)")
}

func codexWith(prompt string) []string {
	return []string{"  Worked for 33s • 16:32", "› " + prompt, "  GPT-6-Sol high · ~/orca/workspaces/x · y", "  ? for shortcuts"}
}

func TestReadComposer(t *testing.T) {
	ours := "[hadron chat #20788] Ada: dispatch"
	cases := []struct {
		name     string
		family   string
		sc       *Screen
		want     Composer
		leftover bool
	}{
		{"a proven-empty Claude composer", "claude", &Screen{Source: "screen", Tail: claudeIdle}, ComposerEmpty, false},
		{"a proven-empty Codex composer", "codex", &Screen{Source: "screen", Tail: codexIdle}, ComposerEmpty, false},
		{"R2 inverts #6736: a Claude draft is held even when the screen proves the composer empty", "claude",
			&Screen{Source: "screen", Tail: claudeIdle, Draft: "ghost text no client showed"}, ComposerDraft, false},
		{"a Codex draft is held", "codex", &Screen{Source: "screen", Tail: codexIdle, Draft: "half a thought"}, ComposerDraft, false},
		{"our own prefix in Codex's draft is our leftover", "codex", &Screen{Source: "screen", Tail: codexIdle, Draft: ours}, ComposerOwn, true},
		{"our own prefix in Claude's draft is our leftover", "claude", &Screen{Source: "screen", Tail: claudeIdle, Draft: ours}, ComposerOwn, true},
		{"a human's text in Claude's box is a draft, held", "claude", &Screen{Source: "screen", Tail: claudeWith("❯ please also check the")}, ComposerDraft, false},
		{"our own text in Claude's box, wrapped, is our leftover", "claude",
			&Screen{Source: "screen", Tail: claudeWith("❯ [hadron chat #20788] Ada:", "  dispatch")}, ComposerOwn, true},
		{"a human's text on Codex's prompt line is a draft", "codex", &Screen{Source: "screen", Tail: codexWith("let me finish")}, ComposerDraft, false},
		{"a numbered approval menu is a question", "claude", &Screen{Source: "screen", Tail: []string{
			"Do you want to proceed?", "❯ 1. Yes", "  2. No, and tell Claude what to do differently (esc)",
		}}, ComposerQuestion, false},
		{"a Codex approval is a question", "codex", &Screen{Source: "screen", Tail: []string{
			"Would you like to run the following command?", "  $ make test", "› 1. Yes, proceed", "  2. No",
		}}, ComposerQuestion, false},
		{"a shell is not a composer", "claude", &Screen{Source: "screen", Tail: []string{"orca$ ls", "README.md", "orca$ "}}, ComposerUnknown, false},
		{"a bare Codex › holds: the composer holds what it does not draw", "codex", &Screen{Source: "screen", Tail: codexWith("")}, ComposerUnknown, false},
		{"a stream read is not a screen", "claude", &Screen{Source: "stream", Tail: claudeIdle}, ComposerUnknown, false},
		{"no screen at all", "claude", nil, ComposerUnknown, false},
		{"text in the transcript never blocks a proven-empty composer", "claude",
			&Screen{Source: "screen", Tail: append([]string{"Do you want to proceed?"}, claudeIdle...)}, ComposerEmpty, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, leftover := ReadComposer(c.sc, c.family, prefix)
			if got != c.want || (leftover != "") != c.leftover {
				t.Fatalf("ReadComposer = %s (leftover %q), want %s (leftover %v)", got, leftover, c.want, c.leftover)
			}
			if c.leftover && Normalize(leftover) != Normalize(ours) {
				t.Fatalf("leftover %q does not normalize to what was typed", leftover)
			}
		})
	}
}

func TestScreenAgentFamily(t *testing.T) {
	for _, c := range []struct {
		tail []string
		want string
	}{
		{claudeIdle, "claude"}, {codexIdle, "codex"}, {[]string{"orca$ echo \"Worked for 33s\"", "orca$ "}, ""},
		{append(append([]string{}, claudeIdle...), codexIdle...), ""},
	} {
		if got := ScreenAgentFamily(&Screen{Source: "screen", Tail: c.tail}); got != c.want {
			t.Errorf("%q: got %q want %q", c.tail[0], got, c.want)
		}
	}
}
