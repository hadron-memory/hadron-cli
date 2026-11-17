package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/spf13/cobra"
)

func nodeAdvice1142(key, code, text string) string {
	b, _ := json.Marshal(map[string]any{"path": key, "code": code, "text": text})
	return string(b)
}

func TestNodeWriteWarningTerminalAndBrokenDiagnostic1142(t *testing.T) {
	code := "future\n\x1b\u202e" + strings.Repeat("c", 300)
	text := "Advice\r\n\x1b\u200b" + strings.Repeat("t", 300) + "TAIL"
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			srv := fakeGraphQL(t, map[string]string{"CreateNode": `{"data":{"createNode":` + nodeJSON + `},"extensions":{"warnings":[` + nodeAdvice1142("createNode", code, text) + `]}}`})
			f, out := testFactory(t)
			teamGitDir(t)
			stderr := &strings.Builder{}
			f.IOStreams.ErrOut = stderr
			if broken {
				f.IOStreams.ErrOut = failingWriter{}
			}
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "add", "-m", "acme.com:kb", "--loc", "test:pin", "--name", "pin", "--server", srv.URL, "--json"})
			if err := root.Execute(); err != nil {
				t.Fatal("diagnostic turned committed write into failure", err)
			}
			if !json.Valid([]byte(out.String())) {
				t.Fatal("stdout no longer JSON")
			}
			if broken {
				return
			}
			line := stderr.String()
			if strings.Count(line, "\n") != 1 || strings.ContainsAny(line, "\r\x1b\u202e\u200b") || strings.Contains(line, "TAIL") {
				t.Fatalf("unsafe/unbounded advice %q", line)
			}
			if utf8.RuneCountInString(line) > len("warning: [] \n")+512 {
				t.Fatalf("safe-cell bounds exceeded: %d", utf8.RuneCountInString(line))
			}
			if !strings.Contains(line, "…") {
				t.Fatal("missing bounded rendering marker")
			}
		})
	}
}

func TestNodeWriteWarningRepeatedAssembledTree1142(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.OperationName != "CreateNode" {
			t.Errorf("extra request %s", req.OperationName)
		}
		body := `{"data":{"createNode":` + nodeJSON + `}}`
		if calls.Add(1) == 1 {
			body = body[:len(body)-1] + `,"extensions":{"warnings":[` + nodeAdvice1142("createNode", "include.pin.unkept", pinWarningText) + `]}}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	f, out := testFactory(t)
	teamGitDir(t)
	stderr := &strings.Builder{}
	f.IOStreams.ErrOut = stderr
	root := NewRootCmd(f)
	type marker struct{}
	original := context.WithValue(context.Background(), marker{}, "retained")
	root.SetContext(original)
	for i := 0; i < 2; i++ {
		root.SetArgs([]string{"node", "add", "-m", "acme.com:kb", "--loc", "test:pin", "--name", "pin", "--server", srv.URL, "--json"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		leaf, _, err := root.Find([]string{"node", "add"})
		if err != nil {
			t.Fatal(err)
		}
		if leaf.Context() != original {
			t.Fatal("command context was not restored")
		}
		if i == 0 && !strings.Contains(stderr.String(), pinWarningText) {
			t.Fatal("first warning absent")
		}
		if i == 1 && stderr.Len() != 0 {
			t.Fatal("prior invocation advice leaked", stderr.String())
		}
		if !json.Valid([]byte(out.String())) {
			t.Fatal("invalid output")
		}
		stderr.Reset()
		out.Reset()
	}
	if calls.Load() != 2 {
		t.Fatalf("%d writes; want exactly2", calls.Load())
	}
}

func TestNodeWriteWarningMultipleWritesAndPartialFailure1142(t *testing.T) {
	for _, failSecond := range []bool{false, true} {
		t.Run(fmt.Sprint(failSecond), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					OperationName string `json:"operationName"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				key := "createNode"
				if calls.Add(1) == 2 {
					key = "updateNode"
				}
				body := `{"data":{"` + key + `":{"id":"n"}},"extensions":{"warnings":[` + nodeAdvice1142(key, "future.code", key+" advice") + `]}}`
				if failSecond && key == "updateNode" {
					body = `{"errors":[{"message":"refused"}],"extensions":{"warnings":[` + nodeAdvice1142(key, "future.code", "wrong success advice") + `]}}`
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			client, err := api.NewClient(srv.URL, "", srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			f, _ := testFactory(t)
			stderr := &strings.Builder{}
			f.IOStreams.ErrOut = stderr
			command := &cobra.Command{Use: "writes", RunE: func(c *cobra.Command, _ []string) error {
				for _, key := range []string{"createNode", "updateNode"} {
					var data map[string]any
					if err := client.MakeRequest(c.Context(), &graphql.Request{Query: "mutation M { " + key + " { id } }", OpName: "M"}, &graphql.Response{Data: &data}); err != nil {
						return err
					}
				}
				return nil
			}}
			installNodeWriteWarnings(command, f)
			err = command.Execute()
			if (err != nil) != failSecond {
				t.Fatalf("err=%v failSecond=%v", err, failSecond)
			}
			want := 2
			if failSecond {
				want = 0
			}
			if strings.Count(stderr.String(), "warning:") != want {
				t.Fatalf("partial/success advice %q", stderr.String())
			}
			if calls.Load() != 2 {
				t.Fatalf("extra requests/retries: %d", calls.Load())
			}
		})
	}
}
