package cmd

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ---- a printed `edge create` remedy runs as printed (#687 slice 2) ----
//
// A partial write names the command that finishes it. Since hadron-server#1300
// dropped the platform-role gate, that command can work for every memory
// writer, but only if it runs exactly as printed: a remedy whose bare locs
// carry no -m is refused locally before it reaches the server.

var printedEdgeCreate = regexp.MustCompile("`(hadron edge create [^`]*)`")

// remedyArgs pulls the `hadron edge create …` command out of an error and
// splits it the way a shell would split what the error printed: words on
// spaces, and a %q-quoted label as one word.
func remedyArgs(t *testing.T, errText string) []string {
	t.Helper()
	m := printedEdgeCreate.FindStringSubmatch(errText)
	if m == nil {
		t.Fatalf("no `hadron edge create …` remedy in %q", errText)
	}
	var args []string
	rest := m[1]
	for rest = strings.TrimSpace(rest); rest != ""; rest = strings.TrimSpace(rest) {
		if rest[0] == '"' {
			end := 1
			for ; end <= len(rest); end++ {
				if q, err := strconv.Unquote(rest[:end]); err == nil {
					args = append(args, q)
					break
				}
			}
			if end > len(rest) {
				t.Fatalf("unterminated quote in remedy %q", m[1])
			}
			rest = rest[end:]
			continue
		}
		word, after, _ := strings.Cut(rest, " ")
		args = append(args, word)
		rest = after
	}
	if len(args) < 3 || args[0] != "hadron" {
		t.Fatalf("remedy %q does not start with `hadron edge create`", m[1])
	}
	return args[1:]
}

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
	gql, _ := queueGraphQL(t, map[string][]string{
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
		"-m", codingMem, "--route", "fix a flaky OTP test", "--description", "d", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("a missing back-edge is a partial write")
	}
	args, sent := runRemedy(t, err.Error())
	if flagValue(args, "--from") != "findings:flaky-otp-timer" || flagValue(args, "--to") != "preflight" {
		t.Errorf("the back-edge runs from the routed node to the index, got %v", args)
	}
	if sent.Name == "" {
		t.Error("the remedy must carry the route's label")
	}
}

func TestCodingPreflightCreateUnroutedRemedyRuns(t *testing.T) {
	gql, _ := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {codingRouterWithBody(flatRouterBody)},
		"GetNode":    {newRouteNodeRead(backEdgeJSON)},
		"CreateNode": {`{"data":{"createNode":` + newRouteNodeJSON + `}}`},
		"CreateEdge": {`{"errors":[{"message":"edge refused"}]}`},
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(preflightCreateArgs(gql.URL))
	err := root.Execute()
	if err == nil {
		t.Fatal("an unrouted node is a partial write")
	}
	args, _ := runRemedy(t, err.Error())
	if flagValue(args, "--to") == "" || flagValue(args, "-m") != codingMem {
		t.Errorf("the remedy must name the new node and the memory, got %v", args)
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
