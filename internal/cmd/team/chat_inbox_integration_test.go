package team

import (
	"context"
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"

	"github.com/hadron-memory/hadron-cli/internal/output"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInboxProductionRegistered(t *testing.T) {
	c := NewCmdTeam(cmdutil.NewFactory())
	found, _, err := c.Find([]string{"chat", "inbox"})
	if err != nil || found.Name() != "inbox" {
		t.Fatalf("production inbox missing: %v %s", err, found.Name())
	}
}

func TestInboxProductionSessionWireAndOrdering(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "hdr_user_fixture")
	dir := t.TempDir()
	t.Setenv(GitDirEnv, dir)
	streams, out, _ := output.Test()
	calls := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			OperationName string                     `json:"operationName"`
			Variables     map[string]json.RawMessage `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
		}
		calls = append(calls, q.OperationName)
		if r.Header.Get(api.SessionHeader) != "s1" {
			t.Errorf("missing exact session on %s", q.OperationName)
		}
		if r.Header.Get("Authorization") != "Bearer hdr_user_fixture" {
			t.Error("wrong credential")
		}
		w.Header().Set("Content-Type", "application/json")
		switch q.OperationName {
		case "SessionInbox":
			if string(q.Variables["workerRefs"]) != `["self"]` || string(q.Variables["appRef"]) != `"app"` {
				t.Error("not scoped to bound worker/App")
			}
			_, _ = io.WriteString(w, `{"data":{"deliveryBatches":{"batches":[{"id":"private-proof","worker":{"id":"self","name":"Self"},"text":"exact server text","ranges":[],"overflow":0}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`)
		case "AcknowledgeDeliveryBatches":
			if !strings.Contains(out.String(), "exact server text") {
				t.Error("ack before output")
			}
			if _, ok := q.Variables["through"]; ok {
				t.Error("full display must omit through")
			}
			if string(q.Variables["batchIds"]) != `["private-proof"]` {
				t.Error("wrong proofs")
			}
			_, _ = io.WriteString(w, `{"data":{"acknowledgeDeliveryBatches":{"acknowledged":1,"unchanged":0}}}`)
		default:
			t.Errorf("unexpected op %s", q.OperationName)
		}
	}))
	defer srv.Close()
	f := cmdutil.NewFactory()
	f.ServerFlag = srv.URL
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	f.IOStreams = streams
	data, _ := json.Marshal(binding{AppID: "app", WorkerID: "self", SessionID: "s1", Server: srv.URL})
	if err := os.WriteFile(filepath.Join(dir, bindingFileName), data, 0600); err != nil {
		t.Fatal(err)
	}
	c := newCmdTeamChatInbox(f)
	c.SetArgs([]string{})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "SessionInbox,AcknowledgeDeliveryBatches" {
		t.Fatal(calls)
	}
	if out.String() != "exact server text\n" {
		t.Fatal(out.String())
	}
}

func TestInboxAdapterRejectsIncompleteAck(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "hdr_user_fixture")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"acknowledgeDeliveryBatches":{"acknowledged":0,"unchanged":0}}}`)
	}))
	defer srv.Close()
	f := cmdutil.NewFactory()
	f.ServerFlag = srv.URL
	s := &sessionInbox{factory: f}
	if _, err := s.Ack(context.Background(), inboxIdentity{SessionID: "s1"}, []string{"private"}); err == nil {
		t.Fatal("accepted missing acknowledgment")
	}
}

func TestInboxProductionBindingAndEmptyCursorRefuseBeforeNetwork(t *testing.T) {
	t.Setenv(GitDirEnv, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	f := cmdutil.NewFactory()
	f.ServerFlag = "https://example.invalid"
	f.IOStreams, _, _ = output.Test()
	for _, args := range [][]string{{}, {"--after="}} {
		c := newCmdTeamChatInbox(f)
		c.SetArgs(args)
		if err := c.Execute(); exitcode.FromError(err) != exitcode.Usage {
			t.Fatalf("args %v: %v", args, err)
		}
	}
}
