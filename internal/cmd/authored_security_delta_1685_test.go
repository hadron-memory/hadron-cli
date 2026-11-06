package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestAuthoredUnavailablePartialRender1685(t *testing.T) {
	for _, status := range []string{"created root but its contract failed", "scaffolding failed after creating A, B", "linked old to replacement; retirement failed"} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", status, jsonMode), func(t *testing.T) {
				cause := gqlerror.List{&gqlerror.Error{Message: "SECRET_TASK", Extensions: map[string]interface{}{"code": "AUTHOR_TASK_BROKEN", "taskAccess": "UNAVAILABLE", "task": "SECRET_TASK"}}}
				err := fmt.Errorf("%s: %w", status, api.MapError(cause))
				f, out := testFactory(t)
				var stderr bytes.Buffer
				f.IOStreams.ErrOut = &stderr
				args := []string{"spec", "new"}
				if jsonMode {
					args = append(args, "--json")
				}
				renderFailure(f, args, err)
				text := out.String() + stderr.String()
				if !strings.Contains(text, status) || strings.Contains(text, "SECRET_") {
					t.Fatalf("partial status or concealment lost: %s", text)
				}
			})
		}
	}
}

func TestAuthoredMalformedAccessConcealed1685(t *testing.T) {
	for _, access := range []interface{}{nil, "readable", "", 1} {
		t.Run(fmt.Sprint(access), func(t *testing.T) {
			ext := map[string]interface{}{"code": "AUTHOR_TASK_BROKEN", "task": "SECRET_TASK", "taskId": "SECRET_ID", "load": map[string]interface{}{"cli": "SECRET_CLI"}}
			if access != nil {
				ext["taskAccess"] = access
			}
			cause := gqlerror.List{&gqlerror.Error{Message: "SECRET_TASK", Extensions: ext}}
			details := api.AuthoringAttestationDetails(cause)
			if len(details) != 2 || details["taskAccess"] != "UNAVAILABLE" {
				t.Fatalf("fail-open details: %v", details)
			}
			for _, jsonMode := range []bool{false, true} {
				f, out := testFactory(t)
				var stderr bytes.Buffer
				f.IOStreams.ErrOut = &stderr
				args := []string{"node", "create"}
				if jsonMode {
					args = append(args, "--json")
				}
				renderFailure(f, args, api.MapError(cause))
				if strings.Contains(out.String()+stderr.String(), "SECRET_") {
					t.Fatalf("leak: %s", out.String())
				}
			}
		})
	}
}

func TestAuthoredMixedAccessConcealed1685(t *testing.T) {
	cause := gqlerror.List{
		&gqlerror.Error{Message: "readable", Extensions: map[string]interface{}{"code": "AUTHORING_ATTESTATION_MISSING", "taskAccess": "READABLE", "task": "public"}},
		&gqlerror.Error{Message: "SECRET_TASK", Extensions: map[string]interface{}{"code": "AUTHOR_TASK_BROKEN", "taskAccess": "UNAVAILABLE", "task": "SECRET_TASK"}},
	}
	for _, jsonMode := range []bool{false, true} {
		f, out := testFactory(t)
		var stderr bytes.Buffer
		f.IOStreams.ErrOut = &stderr
		args := []string{"node", "create"}
		if jsonMode {
			args = append(args, "--json")
		}
		renderFailure(f, args, api.MapError(cause))
		if strings.Contains(out.String()+stderr.String(), "SECRET_") {
			t.Fatalf("mixed response leaked: %s", out.String())
		}
	}
}

func TestAuthoredNewPartialExecution1685(t *testing.T) {
	refusal := `{"errors":[{"message":"SECRET_TASK","extensions":{"code":"AUTHOR_TASK_BROKEN","taskAccess":"UNAVAILABLE","task":"SECRET_TASK","load":{"cli":"SECRET_CLI"}}}]}`
	success := `{"data":{"createSpecNode":{"id":"n1","memoryId":"mem1","loc":"x","name":"X","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`
	for _, path := range []bool{false, true} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("path%t/json%t", path, jsonMode), func(t *testing.T) {
				find := `{"data":{"nodes":[` + specNodeList("msg", `["spec"]`) + `]}}`
				args := []string{"spec", "new", "--new-module", "--module", "brd", "--title", "Send", "--no-edges", "-m", specMem}
				want := "created"
				if path {
					find = `{"data":{"nodes":[]}}`
					args = []string{"spec", "new", "msg:010:01", "--new-path", "--title", "Send", "--no-edges", "-m", specMem}
					want = "after creating"
				}
				srv, _ := queueGraphQL(t, map[string][]string{"FindNodes": {find, `{"data":{"nodes":[]}}`}, "CreateSpecNode": {success, refusal}})
				f, out := testFactory(t)
				var stderr bytes.Buffer
				f.IOStreams.ErrOut = &stderr
				args = append(args, "--server", srv.URL, "--authored-with", "task1", "--authored-revision", "4")
				if jsonMode {
					args = append(args, "--json")
				}
				root := NewRootCmd(f)
				root.SetArgs(args)
				err := root.Execute()
				if err == nil {
					t.Fatal("expected partial refusal")
				}
				renderFailure(f, args, err)
				text := out.String() + stderr.String()
				if !strings.Contains(text, want) || strings.Contains(text, "SECRET_") {
					t.Fatalf("partial output: %s", text)
				}
			})
		}
	}
}

func TestAuthoredRevisionRange1685(t *testing.T) {
	for _, revision := range []string{"2147483648", "-2147483649"} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "create", "-m", "acme.com::kb", "--loc", "x", "--name", "X", "--authored-with", "task1", "--authored-revision", revision, "--server", "http://127.0.0.1:1"})
		err := root.Execute()
		if exitCodeFor(err) != 2 || !strings.Contains(err.Error(), "GraphQL integer") {
			t.Fatalf("range checked after transport: %v", err)
		}
	}
}
