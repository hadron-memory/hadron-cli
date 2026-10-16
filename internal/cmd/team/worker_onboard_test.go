package team

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/teamrouter"
)

type onboardClient func(context.Context, *graphql.Request, *graphql.Response) error

func (c onboardClient) MakeRequest(ctx context.Context, r *graphql.Request, out *graphql.Response) error {
	return c(ctx, r, out)
}

func TestOnboardQueueTerminalFailures(t *testing.T) {
	step := onboardOptions{repo: "org/repo", base: "main", branch: "iris/task", label: "iris", family: "codex"}.steps()[0]
	for _, tc := range []struct{ name, status, result, want string }{
		{"failed", "FAILED", `null`, "failed"},
		{"wrong-result", "SUCCEEDED", `{"worktree":"other/worker"}`, "unexpected worktree"},
		{"pending", "COLLECTED", `null`, "deadline exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, _ := json.Marshal(step.Args)
			client := onboardClient(func(_ context.Context, r *graphql.Request, out *graphql.Response) error {
				if r.OpName != "HostOperations" {
					t.Fatalf("unexpected mutation %s", r.OpName)
				}
				return json.Unmarshal([]byte(`{"hostOperations":{"items":[{"id":"op1","type":"worktree.create","hostId":"orca1","workerId":"w1","args":`+string(args)+`,"status":"`+tc.status+`","result":`+tc.result+`}],"hasNextPage":false}}`), out.Data)
			})
			q := onboardQueue{client: client, app: "app1", host: "orca1", worker: "w1"}
			err := q.ensure(context.Background(), step, 20*time.Millisecond)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %s, got %v", tc.want, err)
			}
			if q.last == nil || q.last.Id != "op1" || string(q.last.Status) != tc.status {
				t.Fatalf("lost freshest record: %+v", q.last)
			}
		})
	}
}

func TestOnboardQueueSearchPaginatesBeforeIssuing(t *testing.T) {
	step := onboardOptions{repo: "org/repo", base: "main", branch: "iris/task", label: "iris", family: "codex"}.steps()[0]
	args, _ := json.Marshal(step.Args)
	reads := 0
	c := onboardClient(func(_ context.Context, r *graphql.Request, out *graphql.Response) error {
		if r.OpName != "HostOperations" {
			t.Fatalf("reissued an older op: %s", r.OpName)
		}
		reads++
		if reads == 1 {
			return json.Unmarshal([]byte(`{"hostOperations":{"items":[],"hasNextPage":true,"endCursor":"page2"}}`), out.Data)
		}
		return json.Unmarshal([]byte(`{"hostOperations":{"items":[{"id":"op1","type":"worktree.create","hostId":"orca1","workerId":"w1","args":`+string(args)+`,"status":"SUCCEEDED","result":{"worktree":"repo/iris"}}],"hasNextPage":false}}`), out.Data)
	})
	q := onboardQueue{client: c, app: "app1", host: "orca1", worker: "w1"}
	if err := q.ensure(context.Background(), step, time.Second); err != nil {
		t.Fatal(err)
	}
	if reads != 2 {
		t.Fatalf("reads=%d", reads)
	}
}

func TestOnboardCapabilityGateRejectsUnprovenRuntime(t *testing.T) {
	for _, raw := range []string{``, `{}`, `{"ok":true}`, `{"ok":true,"result":{"runtime":{"reachable":false}}}`, `{"ok":false,"result":{"runtime":{"reachable":true}}}`} {
		err := onboardOrcaAvailable(context.Background(), func(context.Context, []string) teamrouter.RunResult { return teamrouter.RunResult{Stdout: raw} })
		if exitcode.FromError(err) != exitcode.Usage {
			t.Fatalf("accepted %q: %v", raw, err)
		}
	}
}

func TestOnboardPlanMatchesServerContractFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/onboard-host-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Revision   string `json:"revision"`
		Operations map[string]struct {
			Required map[string]string `json:"required"`
			Optional map[string]string `json:"optional"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Revision) != 40 {
		t.Fatal("fixture lacks source revision")
	}
	for _, step := range (onboardOptions{repo: "org/repo", base: "main", branch: "iris/task", label: "iris", family: "codex"}).steps() {
		spec, ok := fixture.Operations[step.Type]
		if !ok {
			t.Fatalf("invented operation %s", step.Type)
		}
		keys := map[string]bool{}
		for k := range step.Args {
			keys[k] = true
		}
		want := map[string]bool{}
		for k := range spec.Required {
			if k != "workerId" {
				want[k] = true
			}
		}
		if !reflect.DeepEqual(keys, want) {
			t.Fatalf("argument drift %s: %v vs %v", step.Type, keys, want)
		}
	}
}
