package cmd

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// ---- a printed `edge create` remedy runs as printed (#687 slice 2) ----
//
// A partial write names the command that finishes it. Since hadron-server#1300
// dropped the platform-role gate, that command can work for every memory
// writer, but only if it runs exactly as printed: a remedy whose bare locs
// carry no -m is refused locally before it reaches the server.

// remedyArgs pulls the `hadron edge create …` command out of an error and
// splits it with a real POSIX shell, so "runs as printed" means what a pasted
// command does: a label quoted for Go rather than the shell would come back
// expanded here, not merely unquoted (#1083 review).
func remedyArgs(t *testing.T, errText string) []string {
	t.Helper()
	line := printedCommand(t, errText, "hadron edge create ")
	args := shellWords(t, line)
	if len(args) < 3 || args[0] != "hadron" {
		t.Fatalf("remedy %q does not start with `hadron edge create`", line)
	}
	return args[1:]
}

// printedCommand is the backtick-wrapped command starting with prefix. The
// closing backtick is the first one outside single quotes and not escaped,
// since a quoted label may itself contain a backtick.
func printedCommand(t *testing.T, text, prefix string) string {
	t.Helper()
	i := strings.Index(text, "`"+prefix)
	if i < 0 {
		t.Fatalf("no `%s…` command in %q", prefix, text)
	}
	rest, quoted := text[i+1:], false
	for j := 0; j < len(rest); j++ {
		switch c := rest[j]; {
		case c == '\'':
			quoted = !quoted
		case c == '\\' && !quoted:
			j++ // an escaped character, such as the \' in '\''
		case c == '`' && !quoted:
			return rest[:j]
		}
	}
	t.Fatalf("unterminated command in %q", text)
	return ""
}

// shellWords is the argv sh gives the command line, one NUL-ended word each.
// HOME is set so an unquoted $HOME shows up as an expansion, never as itself.
func shellWords(t *testing.T, line string) []string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX sh to split the printed command")
	}
	cmd := exec.Command(sh, "-c", `printf '%s\0' `+line)
	cmd.Env = []string{"HOME=/expanded-home", "PATH=/nonexistent"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh could not split %q: %v", line, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

// shellyLabel carries every shell metacharacter a quoted label must keep.
const shellyLabel = `fix $HOME and ` + "`id`" + ` "quoted" isn't \ $(x)`

// runRemedy executes the printed remedy against a server that resolves every
// ref and accepts the edge, and returns the createEdge variables it sent.
func runRemedy(t *testing.T, errText string) (args []string, sent struct {
	SourceRef string `json:"sourceRef"`
	TargetRef string `json:"targetRef"`
	Name      string `json:"name"`
}) {
	t.Helper()
	args = remedyArgs(t, errText)
	gql, captured := queueGraphQL(t, map[string][]string{
		"ResolveUrn": {
			`{"data":{"resolveUrn":{"id":"n_from","kind":"node","memoryId":"mem1"}}}`,
			`{"data":{"resolveUrn":{"id":"n_to","kind":"node","memoryId":"mem1"}}}`,
		},
		"CreateEdge": {`{"data":{"createEdge":{"id":"e_fix","name":"x","loc":"l","isRunnable":false,"priority":0,` +
			`"source":{"id":"n_from","loc":"a"},"target":{"id":"n_to","loc":"b"}}}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{}, args...), "--server", gql.URL))
	if err := root.Execute(); err != nil {
		t.Fatalf("the printed remedy `hadron %s` does not run: %v", strings.Join(args, " "), err)
	}
	vars := captured["CreateEdge"]
	if len(vars) != 1 {
		t.Fatalf("the remedy should create exactly one edge, sent %d", len(vars))
	}
	if err := json.Unmarshal(vars[0], &sent); err != nil {
		t.Fatalf("decoding createEdge variables: %v", err)
	}
	if sent.SourceRef != "n_from" || sent.TargetRef != "n_to" {
		t.Errorf("the remedy should wire the resolved endpoints, sent %+v", sent)
	}
	return args, sent
}

func flagValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func TestCodingPreflightRouteBackEdgeRemedyRuns(t *testing.T) {
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody)},
		"GetNode":    {codingNodeJSON("n_target", "findings:flaky-otp-timer", "", "")},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem1"}}}`},
		"CreateEdge": {
			`{"data":{"createEdge":` + newRouteEdgeJSON + `}}`, // forward route lands
			`{"errors":[{"message":"back-edge refused"}]}`,     // back-edge does not
		},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "route", "findings:flaky-otp-timer",
		"-m", codingMem, "--route", shellyLabel, "--description", "d", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a missing back-edge is a partial write")
	}
	args, sent := runRemedy(t, err.Error())
	if flagValue(args, "--from") != "findings:flaky-otp-timer" || flagValue(args, "--to") != "preflight" {
		t.Errorf("the back-edge runs from the routed node to the index, got %v", args)
	}
	if want := sentEdgeName(t, captured["CreateEdge"][1]); sent.Name != want {
		t.Errorf("the remedy must carry the route's label as written: sent %q, want %q", sent.Name, want)
	}
}

// sentEdgeName is the name a captured createEdge call sent.
func sentEdgeName(t *testing.T, vars json.RawMessage) string {
	t.Helper()
	var v struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(vars, &v); err != nil || v.Name == "" {
		t.Fatalf("decoding createEdge name from %s: %v", vars, err)
	}
	return v.Name
}

func TestCodingPreflightCreateUnroutedRemedyRuns(t *testing.T) {
	gql, captured := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody)},
		"GetNode":    {newRouteNodeRead(backEdgeJSON)},
		"CreateNode": {`{"data":{"createNode":` + newRouteNodeJSON + `}}`},
		"CreateEdge": {`{"errors":[{"message":"edge refused"}]}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	args := preflightCreateArgs(gql.URL)
	for i := range args {
		if args[i] == "--route" {
			args[i+1] = shellyLabel
		}
	}
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		t.Fatal("an unrouted node is a partial write")
	}
	args, sent := runRemedy(t, err.Error())
	if flagValue(args, "--to") == "" || flagValue(args, "-m") != codingMem {
		t.Errorf("the remedy must name the new node and the memory, got %v", args)
	}
	if want := sentEdgeName(t, captured["CreateEdge"][0]); sent.Name != want {
		t.Errorf("the remedy must carry the route's label as written: sent %q, want %q", sent.Name, want)
	}
}

func TestCodingReviewCreateUnwiredRemedyRuns(t *testing.T) {
	newNode := `{"id":"n_new","memoryId":"mem1","loc":"review:orphan","name":"orphan",
		"nodeType":"info","tags":["review"],"seq":null,"isRunnable":false,"updatedAt":"2026-08-04T00:00:00Z"}`
	gql, _ := queueGraphQL(t, map[string][]string{
		"GetNode": {
			codingRootJSON("review", "", ""),
			codingNodeJSON("n_new", "review:orphan", "", ""), // no edge came back
		},
		"CreateReviewNode": {`{"data":{"createReviewNode":` + newNode + `}}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "create", "orphan", "-m", codingMem,
		"--trigger", "a thing changes", "--description", "d", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("an unwired check is a partial write")
	}
	args, _ := runRemedy(t, err.Error())
	if flagValue(args, "--from") != "review:orphan" || flagValue(args, "--to") != "review" {
		t.Errorf("the remedy wires the new check to its review parent, got %v", args)
	}
}
