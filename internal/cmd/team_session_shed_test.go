package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#952: `team session shed` — the checkpoint node, the recovery
// prompt and the resume claim in one step. It is a hook command, so it never
// blocks; and the claim is written LAST so a failed node write cannot leave one.

type shedRig struct {
	calls    []string
	override map[string]string
	captured map[string]json.RawMessage
}

func (r *shedRig) count(op string) int {
	n := 0
	for _, c := range r.calls {
		if c == op {
			n++
		}
	}
	return n
}

// shedServer answers every read with a healthy value. GetNode echoes the body
// the CLI sent to CreateNode, because the read-back must verify it.
func shedServer(t *testing.T, override map[string]string) (string, *shedRig) {
	t.Helper()
	rig := &shedRig{override: override}
	var captured map[string]json.RawMessage
	respond := func(op string) string {
		rig.calls = append(rig.calls, op)
		if v, ok := rig.override[op]; ok {
			var c struct {
				Input struct{ Loc string } `json:"input"`
			}
			_ = json.Unmarshal(captured["CreateNode"], &c)
			return strings.ReplaceAll(v, "{LOC}", c.Input.Loc)
		}
		switch op {
		case "GetWorker":
			return `{"data":{"worker":` + irisWorkerJSON + `}}`
		case "TeamChatReadHead":
			return `{"data":{"teamChatMessages":{"items":[{"seq":41435}]}}}`
		case "TeamDefaultChannel":
			return `{"data":{"app":{"id":"capp1","defaultChannel":{"id":"ch1"}}}}`
		case "CheckChatCursor":
			return `{"data":{"channelReadState":{"channelId":"ch1","channelIncarnation":1,"cursorIncarnation":1,"lastSeenSeq":41432}}}`
		case "TeamWorkItems":
			return `{"data":{"teamWorkItems":{"total":1,"items":[{"nodeId":"n1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris","tool":"github","kind":"issue","ref":"hadron-memory/hadron-cli#952","action":"started","at":"2026-10-27T20:00:00Z","detail":null,"model":null}]}}}`
		case "GetWorkerContinuity":
			return `{"data":{"worker":{"continuity":{"status":"CURRENT","handoff":{"urn":"hrn:node:acme.com:iris:sheds:old","loc":"sheds:old","content":null,"createdAt":"2026-10-20T00:00:00Z"},"previousSession":null}}}}`
		case "CreateNode":
			var v struct {
				Input struct{ Loc, Content string } `json:"input"`
			}
			_ = json.Unmarshal(captured["CreateNode"], &v)
			return `{"data":{"createNode":{"id":"nd1","memoryId":"mw1","loc":"` + v.Input.Loc + `","name":"x","nodeType":"info","tags":[],"seq":1,"isRunnable":false,"role":null,"updatedAt":"2026-10-27T21:00:00Z"}}}`
		case "GetNode":
			var v struct {
				Input struct{ Loc, Content string } `json:"input"`
			}
			_ = json.Unmarshal(captured["CreateNode"], &v)
			c, _ := json.Marshal(v.Input.Content)
			return `{"data":{"node":{"id":"nd1","urn":"hrn:node:acme.com:iris:` + v.Input.Loc + `","loc":"` + v.Input.Loc + `","name":"x","content":` + string(c) + `}}}`
		case "CreateResumeClaim":
			return `{"data":{"createResumeClaim":{"sessionId":"s-new","workerId":"wkr1","expiresAt":"2026-10-15T12:01:00Z"}}}`
		}
		return `{"data":{}}`
	}
	gql, c := captureGraphQLFunc(t, respond)
	captured = c
	rig.captured = c
	return gql.URL, rig
}

func runShed(t *testing.T, url string, args ...string) (string, string, error) {
	t.Helper()
	f, out := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "session", "shed", "--server", url}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func TestSessionShedWritesNodeThenClaimLast(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	out, _, err := runShed(t, url, "--json")
	if err != nil {
		t.Fatalf("shed: %v", err)
	}
	var d struct {
		Status string  `json:"status"`
		Node   *string `json:"node"`
		Claim  string  `json:"claim"`
	}
	if e := json.Unmarshal([]byte(out), &d); e != nil || d.Status != "written" || d.Claim != "written" || d.Node == nil {
		t.Fatalf("want written/written with a node, got %s (%v)", out, e)
	}
	if strings.Contains(out, "2026-10-15T12:01:00Z") || strings.Contains(strings.ToLower(out), "expires") {
		t.Errorf("no claim material may be echoed: %s", out)
	}
	last := rig.calls[len(rig.calls)-1]
	if last != "CreateResumeClaim" || rig.count("CreateResumeClaim") != 1 {
		t.Errorf("the claim must be the last call, once; calls=%v", rig.calls)
	}
	if i, j := indexOf(rig.calls, "CreateNode"), indexOf(rig.calls, "GetNode"); i < 0 || j < i {
		t.Errorf("the node is created, then read back, before any claim: %v", rig.calls)
	}
	var in struct {
		Input struct{ MemoryId, Loc, Content string } `json:"input"`
	}
	_ = json.Unmarshal(rig.captured["CreateNode"], &in)
	if in.Input.MemoryId != "mw1" || !strings.HasPrefix(in.Input.Loc, "sheds:") {
		t.Errorf("the node goes in the worker's own memory under sheds:, got %+v", in.Input)
	}
	pos := -1
	for _, h := range []string{"## Identity", "## Watermark", "## Open work", "## Briefing pointers", "## Recovery prompt"} {
		p := strings.Index(in.Input.Content, h)
		if p <= pos {
			t.Fatalf("heading %q missing or out of order in:\n%s", h, in.Input.Content)
		}
		pos = p
	}
	if !strings.Contains(in.Input.Content, "#41435") || !strings.Contains(in.Input.Content, "#41432") {
		t.Errorf("head and cursor must be in the Watermark:\n%s", in.Input.Content)
	}
}

// A failed step 1-6 must stop before step 8: no claim, exit 0.
func TestSessionShedFailureBeforeClaimLeavesNoClaim(t *testing.T) {
	cases := map[string]map[string]string{
		"worker read":           {"GetWorker": `{"errors":[{"message":"boom"}]}`},
		"node write":            {"CreateNode": `{"errors":[{"message":"boom"}]}`},
		"read-back":             {"GetNode": `{"data":{"node":null}}`},
		"read-back loc differs": {"GetNode": `{"data":{"node":{"id":"nd1","urn":"hrn:node:a:b:c","loc":"sheds:x","name":"x","content":"c"}}}`},
		"read-back differs":     {"GetNode": `{"data":{"node":{"id":"nd1","urn":"hrn:node:a:b:c","loc":"{LOC}","name":"x","content":"truncated"}}}`},
		"empty create":          {"CreateNode": `{"data":{"createNode":null}}`},
	}
	for name, override := range cases {
		for _, mode := range [][]string{{}, {"--auto"}} {
			t.Run(name+strings.Join(mode, ""), func(t *testing.T) {
				bindWorktree(t)
				url, rig := shedServer(t, override)
				_, errOut, err := runShed(t, url, mode...)
				if err != nil {
					t.Fatalf("shed must exit 0, got %v", err)
				}
				if rig.count("CreateResumeClaim") != 0 {
					t.Errorf("a claim was written after a failed step; calls=%v", rig.calls)
				}
				if !strings.Contains(errOut, "team session shed:") || strings.Contains(errOut, "boom") {
					t.Errorf("one sanitized stderr line, no server text: %q", errOut)
				}
			})
		}
	}
}

func TestSessionShedClaimFailureKeepsTheNode(t *testing.T) {
	bindWorktree(t)
	url, _ := shedServer(t, map[string]string{"CreateResumeClaim": `{"errors":[{"message":"boom"}]}`})
	out, _, err := runShed(t, url, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if d := shedJSON(t, out); d.Claim != "failed" || d.Status != "written" {
		t.Errorf("want a written node with claim failed: %s", out)
	}
}

func TestSessionShedOneFailedReadDegradesOnlyThatField(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, map[string]string{"TeamChatReadHead": `{"errors":[{"message":"denied"}]}`})
	if _, _, err := runShed(t, url); err != nil {
		t.Fatal(err)
	}
	var in struct {
		Input struct{ Content string } `json:"input"`
	}
	_ = json.Unmarshal(rig.captured["CreateNode"], &in)
	if !strings.Contains(in.Input.Content, "Chat head: unavailable: chat head read failed") {
		t.Errorf("the failed field says unavailable:\n%s", in.Input.Content)
	}
	if !strings.Contains(in.Input.Content, "#41432") || rig.count("CreateResumeClaim") != 1 {
		t.Errorf("the other fields and the claim are unaffected:\n%s", in.Input.Content)
	}
}

func TestSessionShedBadFlag(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	if _, errOut, err := runShed(t, url, "--auto", "--cooldwon", "5m"); err != nil || !strings.Contains(errOut, "team session shed:") {
		t.Errorf("--auto must exit 0 on a bad flag with one line on stderr: err=%v stderr=%q", err, errOut)
	}
	if _, _, err := runShed(t, url, "--auto", "--cooldown", "banana"); err != nil {
		t.Errorf("--auto must exit 0 on a bad flag value: %v", err)
	}
	if _, _, err := runShed(t, url, "--cooldwon", "5m"); exitCodeFor(err) != exitcode.Usage {
		t.Errorf("manual mode keeps the usage exit code, got %v", err)
	}
	if len(rig.calls) != 0 {
		t.Errorf("a refused command line sends nothing: %v", rig.calls)
	}
}

func TestSessionShedPrintPromptMutatesNothing(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	out, _, err := runShed(t, url, "--print-prompt")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"CreateNode", "CreateResumeClaim"} {
		if rig.count(op) != 0 {
			t.Errorf("--print-prompt called %s", op)
		}
	}
	if !strings.Contains(out, "whoami --check") || !strings.Contains(out, "team session start") {
		t.Errorf("the prompt names the recovery commands: %s", out)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HADRON_TEAM_GIT_DIR"), "hadron-team-shed.json")); err == nil {
		t.Error("--print-prompt must not stamp the cooldown")
	}
	rig2url, rig2 := shedServer(t, nil)
	if _, _, err := runShed(t, rig2url, "--print-prompt", "--offline"); err != nil || len(rig2.calls) != 0 {
		t.Errorf("--offline makes no network call: err=%v calls=%v", err, rig2.calls)
	}
}

func TestSessionShedCooldown(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	if _, _, err := runShed(t, url, "--auto"); err != nil {
		t.Fatal(err)
	}
	out, _, _ := runShed(t, url, "--auto", "--json")
	if d := shedJSON(t, out); d.Status != "skipped" || d.Claim != "written" {
		t.Errorf("second auto shed: skipped, claim already written: %s", out)
	}
	if rig.count("CreateNode") != 1 || rig.count("CreateResumeClaim") != 1 {
		t.Errorf("a skipped shed writes nothing: %v", rig.calls)
	}
	// Manual ignores the cooldown (a different minute is not guaranteed, so
	// only the node write is asserted to be attempted again).
	_, _, _ = runShed(t, url)
	if rig.count("CreateNode") != 2 {
		t.Errorf("manual shed ignores the cooldown: %v", rig.calls)
	}
}

func TestSessionShedCooldownRetriesOnlyAFailedClaim(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, map[string]string{"CreateResumeClaim": `{"errors":[{"message":"boom"}]}`})
	_, _, _ = runShed(t, url, "--auto")
	delete(rig.override, "CreateResumeClaim")
	out, _, _ := runShed(t, url, "--auto", "--json")
	if d := shedJSON(t, out); d.Status != "skipped" || d.Claim != "written" {
		t.Errorf("the retry writes the claim: %s", out)
	}
	if rig.count("CreateNode") != 1 || rig.count("CreateResumeClaim") != 2 {
		t.Errorf("retry only the claim: %v", rig.calls)
	}
}

func TestSessionShedCooldownResetsForAnotherSession(t *testing.T) {
	dir := teamGitDir(t)
	write := func(id string) {
		b := strings.Replace(bindingWithTeamFixture, `"s-new"`, `"`+id+`"`, 1)
		if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	url, rig := shedServer(t, nil)
	write("s-one")
	_, _, _ = runShed(t, url, "--auto")
	write("s-two")
	_, _, _ = runShed(t, url, "--auto")
	if rig.count("CreateNode") != 2 {
		t.Errorf("another session is not in the cooldown: %v", rig.calls)
	}
}

// The prompt is generated from a command table; every command and flag it cites
// must exist in the registered tree.
func TestSessionShedPromptCitesOnlyRegisteredCommands(t *testing.T) {
	bindWorktree(t)
	url, _ := shedServer(t, nil)
	out, _, err := runShed(t, url, "--print-prompt", "--offline")
	if err != nil {
		t.Fatal(err)
	}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	found := 0
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, "`hadron ")
		if i < 0 {
			continue
		}
		cited := line[i+len("`hadron "):]
		cited = cited[:strings.Index(cited, "`")]
		var path, flags []string
		for _, w := range strings.Fields(cited) {
			switch {
			case strings.HasPrefix(w, "-"):
				flags = append(flags, w)
			case strings.HasPrefix(w, "<"), len(flags) > 0:
			default:
				path = append(path, w)
			}
		}
		c, rest, ferr := root.Find(path)
		if ferr != nil || c == root {
			t.Errorf("prompt cites unknown command %q", cited)
			continue
		}
		_ = rest
		for _, fl := range flags {
			name := strings.TrimLeft(fl, "-")
			known := c.Flags().Lookup(name) != nil || c.InheritedFlags().Lookup(name) != nil
			if !strings.HasPrefix(fl, "--") {
				known = c.Flags().ShorthandLookup(name) != nil || c.InheritedFlags().ShorthandLookup(name) != nil
			}
			if !known {
				t.Errorf("prompt cites unknown flag %s on %q", fl, c.CommandPath())
			}
		}
		found++
	}
	if found < 4 {
		t.Errorf("expected at least 4 cited commands, found %d in:\n%s", found, out)
	}
	if !strings.Contains(out, "Exit 2 means CHOOSE") {
		t.Errorf("the CHOOSE exit code must come from exitcode.Usage: %s", out)
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

type shedResult struct {
	Status string `json:"status"`
	Claim  string `json:"claim"`
}

func shedJSON(t *testing.T, out string) shedResult {
	t.Helper()
	var d shedResult
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("not JSON: %q", out)
	}
	return d
}

// An empty flag is not an absent one: --transcript="$UNSET" must neither be
// dropped silently nor reach a side effect in manual mode.
func TestSessionShedEmptyTranscriptAndNegativeCooldown(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	for _, args := range [][]string{{"--transcript="}, {"--transcript", "  "}, {"--cooldown=-1s"}} {
		if _, _, err := runShed(t, url, args...); exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v: manual mode must refuse with the usage code, got %v", args, err)
		}
	}
	if len(rig.calls) != 0 {
		t.Errorf("the refusal comes before any call: %v", rig.calls)
	}
	if _, _, err := runShed(t, url, "--auto", "--transcript="); err != nil {
		t.Fatalf("--auto must not block: %v", err)
	}
	var in struct {
		Input struct{ Content string } `json:"input"`
	}
	_ = json.Unmarshal(rig.captured["CreateNode"], &in)
	if !strings.Contains(in.Input.Content, "unavailable: --transcript was empty") {
		t.Errorf("under --auto the node says the tail was unavailable:\n%s", in.Input.Content)
	}
}

func TestSessionShedTranscriptTailIsBoundedAndWholeLines(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	path := filepath.Join(t.TempDir(), "t.jsonl")
	big := strings.Repeat("0123456789", 2000) + "\nlast line\n"
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runShed(t, url, "--transcript", path); err != nil {
		t.Fatal(err)
	}
	var in struct {
		Input struct{ Content string } `json:"input"`
	}
	_ = json.Unmarshal(rig.captured["CreateNode"], &in)
	i := strings.Index(in.Input.Content, "## Transcript tail")
	if i < 0 || !strings.Contains(in.Input.Content[i:], "last line") || len(in.Input.Content[i:]) > 8<<10+64 {
		t.Errorf("tail missing or unbounded (%d bytes after the heading)", len(in.Input.Content)-i)
	}
}
