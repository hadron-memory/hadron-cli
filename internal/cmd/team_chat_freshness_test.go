package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

// Existing read fixtures model a consistent independent baseline. Dedicated
// freshness cases below deliberately override this operation to model drift.
func teamReadHeadFixture(pages ...string) string {
	head := 0
	for _, page := range pages {
		var r struct {
			Data struct {
				Chat struct {
					Items []struct {
						Seq int `json:"seq"`
					} `json:"items"`
				} `json:"teamChatMessages"`
			} `json:"data"`
		}
		_ = json.Unmarshal([]byte(page), &r)
		for _, m := range r.Data.Chat.Items {
			if m.Seq > head {
				head = m.Seq
			}
		}
	}
	items := ""
	if head > 0 {
		items = fmt.Sprintf(`{"seq":%d}`, head)
	}
	return fmt.Sprintf(`{"data":{"app":{"id":"capp100000000000000000000","defaultChannel":{"id":"ch1","lastSeq":%d}},"teamChatMessages":{"items":[%s]}}}`, head, items)
}

func TestTeamChatReadDetectsStalePages(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		page  string
		stale bool
	}{
		{"empty forward", []string{"--since", "0"}, teamChatPage(0), true},
		{"short forward", []string{"--since", "0", "--limit", "3"}, teamChatPage(1, 1), true},
		{"stale tail", nil, teamChatPage(1, 1), true},
		{"portable tail before max", []string{"--before", "2147483647"}, teamChatPage(1, 1), true},
		{"newest bounded forward", []string{"--since", "0", "--before", "10", "--limit", "1"}, teamChatPage(1, 1), true},
		{"full forward can have more", []string{"--since", "0", "--limit", "1"}, teamChatPage(2, 1), false},
		{"fresh forward with seq gap", []string{"--since", "0"}, teamChatPage(1, 2), false},
		{"fresh tail", nil, teamChatPage(2, 1, 2), false},
		{"already beyond head", []string{"--since", "20"}, teamChatPage(0), false},
		{"filtered tail omits head legitimately", []string{"--mentions", "Ada"}, teamChatPage(1, 1), false},
		{"backward window omits head legitimately", []string{"--before", "2"}, teamChatPage(1, 1), false},
		{"bounded forward", []string{"--since", "0", "--before", "2"}, teamChatPage(1, 1), false},
		{"post after baseline", nil, teamChatPage(3, 1, 2, 3), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			r := chatReadResponses()
			r["TeamChatMessages"] = tc.page
			r["TeamChatReadHead"] = teamReadHeadFixture(teamChatPage(2, 2))
			srv, calls := attnServer(t, r)
			setTeamBindingServer(t, srv.URL)
			bindingPath := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
			beforeBinding, err := os.ReadFile(bindingPath)
			if err != nil {
				t.Fatal(err)
			}
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"team", "chat", "read", "--json", "--server", srv.URL}
			root.SetArgs(append(args, tc.args...))
			err = root.Execute()
			want := 0
			if tc.stale {
				want = 5
			}
			if got := exitCodeFor(err); got != want {
				t.Fatalf("exit=%d want=%d err=%v output=%s", got, want, err, out.String())
			}
			var dto struct {
				ReadState struct {
					Head       int  `json:"head"`
					ReadCursor *int `json:"readCursor"`
					Stale      bool `json:"suspectedStale"`
				} `json:"readState"`
			}
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if dto.ReadState.Head != 2 || dto.ReadState.Stale != tc.stale || dto.ReadState.ReadCursor == nil {
				t.Fatalf("state=%+v", dto)
			}
			for i, c := range *calls {
				if c.Op == "TeamChatReadHead" && i != 0 {
					t.Fatalf("baseline must be first, got calls=%+v", *calls)
				}
				if c.Op == "MarkOwnTeamChatRead" && tc.stale {
					t.Fatal("stale page marked read")
				}
				if c.Op == "TeamChatReadHead" && c.Session != "" {
					t.Fatal("baseline attributed before delivery")
				}
			}
			if tc.stale {
				afterBinding, readErr := os.ReadFile(bindingPath)
				if readErr != nil || string(afterBinding) != string(beforeBinding) {
					t.Fatalf("stale read changed local binding: err=%v", readErr)
				}
			}
			if tc.stale && !strings.Contains(err.Error(), "nothing marked read") {
				t.Fatalf("missing remedy: %v", err)
			}
		})
	}
}

func TestTeamChatReadProbeFailureDoesNotReadOrMark(t *testing.T) {
	writeTeamBinding(t)
	r := chatReadResponses()
	r["TeamChatReadHead"] = gqlErrorJSON("FORBIDDEN")
	srv, calls := attnServer(t, r)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--since", "0", "--server", srv.URL})
	if got := exitCodeFor(root.Execute()); got != 8 {
		t.Fatalf("exit=%d", got)
	}
	if out.Len() != 0 {
		t.Fatalf("failed probe rendered a read: %s", out.String())
	}
	for _, c := range *calls {
		if c.Op == "TeamChatMessages" || c.Op == "MarkOwnTeamChatRead" {
			t.Fatalf("failed probe proceeded: %+v", *calls)
		}
	}
}

func TestTeamChatReadCursorIsServerStateAndAllocatorGapsAreAllowed(t *testing.T) {
	writeTeamBinding(t)
	r := chatReadResponses()
	// The allocator is beyond the latest surviving message, legitimately.
	r["TeamChatReadMetadata"] = strings.Replace(teamReadHeadFixture(teamChatPage(2, 2)), `"lastSeq":2`, `"lastSeq":20`, 1)
	r["ChannelReadState"] = `{"data":{"channelReadState":{"channelId":"ch1","lastSeenSeq":17,"updatedAt":"2026-10-05T00:00:00Z"}}}`
	srv, _ := attnServer(t, r)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "cursor #17; channel head #2 (allocated #20)") {
		t.Fatalf("missing server cursor / head: %s", out.String())
	}
}

func TestTeamChatReadCursorFailureIsVisibleAndDoesNotForgeZero(t *testing.T) {
	writeTeamBinding(t)
	r := chatReadResponses()
	r["ChannelReadState"] = gqlErrorJSON("FEATURE_NOT_AVAILABLE")
	srv, _ := attnServer(t, r)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	var notes strings.Builder
	f.IOStreams.ErrOut = &notes
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"readCursor": null`) || !strings.Contains(notes.String(), "cursor unavailable") {
		t.Fatalf("out=%s notes=%s", out.String(), notes.String())
	}
}

// Exact argv/seq shapes from cli#801 (the original requests are no longer
// reproducible live). Separate operations model the observed disagreement.
func TestTeamChatReadOriginalIncidents(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		head int
		page string
	}{
		{"Dan empty since5999 limit30", []string{"--since", "5999", "--limit", "30"}, 6178, teamChatPage(0)},
		{"Cody tail6241 missing own6242", nil, 6242, teamChatPage(6241, 6241)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			r := chatReadResponses()
			r["TeamChatMessages"] = tc.page
			r["TeamChatReadHead"] = teamReadHeadFixture(teamChatPage(tc.head, tc.head))
			srv, calls := attnServer(t, r)
			setTeamBindingServer(t, srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"team", "chat", "read", "--json", "--server", srv.URL}, tc.args...))
			if got := exitCodeFor(root.Execute()); got != 5 {
				t.Fatalf("exit=%d output=%s", got, out.String())
			}
			if !strings.Contains(out.String(), `"suspectedStale": true`) {
				t.Fatalf("missing JSON diagnostic: %s", out.String())
			}
			for _, c := range *calls {
				if c.Op == "MarkOwnTeamChatRead" {
					t.Fatal("incident marked read")
				}
			}
		})
	}
}

func TestTeamChatReadOptionalMetadataNeverDeniesReadableChat(t *testing.T) {
	for _, tc := range []struct {
		name, metadata, token string
		bound                 bool
	}{
		{"App key forbidden App field", gqlErrorJSON("FORBIDDEN"), "hdr_app_test", false},
		{"cross org AppMember forbidden App field", gqlErrorJSON("FORBIDDEN"), "hdr_user_test", true},
		{"hidden App metadata", `{"data":{"app":null}}`, "hdr_user_test", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bound {
				writeTeamBinding(t)
			} else {
				teamGitDir(t)
			}
			r := chatReadResponses()
			r["TeamChatReadMetadata"] = tc.metadata
			srv, calls := attnServer(t, r)
			if tc.bound {
				setTeamBindingServer(t, srv.URL)
			}
			f, out := testFactory(t)
			t.Setenv("HADRON_TOKEN", tc.token)
			var notes strings.Builder
			f.IOStreams.ErrOut = &notes
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--json", "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("readable chat denied: %v", err)
			}
			var dto struct {
				Messages  []json.RawMessage `json:"messages"`
				ReadState struct {
					Head      *int `json:"head"`
					Allocated *int `json:"allocatedHead"`
					Cursor    *int `json:"readCursor"`
				} `json:"readState"`
			}
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if len(dto.Messages) != 2 || dto.ReadState.Head == nil || *dto.ReadState.Head != 2 || dto.ReadState.Allocated != nil || dto.ReadState.Cursor != nil {
				t.Fatalf("state forged or chat missing: %s", out.String())
			}
			if !strings.Contains(notes.String(), "metadata unavailable") {
				t.Fatalf("missing diagnostic: %s", notes.String())
			}
			for _, c := range *calls {
				if c.Op == "ChannelReadState" || c.Op == "MarkOwnTeamChatRead" {
					t.Fatalf("optional metadata failure leaked into bookkeeping: %+v", *calls)
				}
			}
		})
	}
}

func TestTeamChatReadOtherAppNeverReadsBindingCursor(t *testing.T) {
	writeTeamBinding(t)
	r := chatReadResponses()
	r["TeamChatReadMetadata"] = strings.Replace(teamReadHeadFixture(teamChatPage(2, 2)), `"id":"capp100000000000000000000"`, `"id":"other-app"`, 1)
	srv, calls := attnServer(t, r)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.Op == "ChannelReadState" {
			t.Fatal("read another App's cursor using this binding")
		}
	}
	if !strings.Contains(out.String(), `"readCursor": null`) {
		t.Fatalf("inapplicable cursor forged: %s", out.String())
	}
}

func TestTeamChatReadMissingPagesDoNotRenderOrMark(t *testing.T) {
	for _, op := range []string{"TeamChatReadHead", "TeamChatMessages"} {
		t.Run(op, func(t *testing.T) {
			writeTeamBinding(t)
			r := chatReadResponses()
			r[op] = `{"data":{"teamChatMessages":null}}`
			srv, calls := attnServer(t, r)
			setTeamBindingServer(t, srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "chat", "read", "--since", "0", "--json", "--server", srv.URL})
			if got := exitCodeFor(root.Execute()); got != 7 {
				t.Fatalf("nil page exit=%d", got)
			}
			if out.Len() != 0 {
				t.Fatalf("nil page rendered: %s", out.String())
			}
			for _, c := range *calls {
				if c.Op == "MarkOwnTeamChatRead" {
					t.Fatal("nil page marked read")
				}
			}
		})
	}
}

func TestTeamChatReadOldServerHeadComparisonIsExplicitlyUnavailable(t *testing.T) {
	teamGitDir(t)
	r := chatReadResponses()
	r["TeamChatReadHead"] = `{"errors":[{"message":"Unknown argument \"beforeSeq\" on field \"Query.teamChatMessages\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	srv, _ := attnServer(t, r)
	f, out := testFactory(t)
	var notes strings.Builder
	f.IOStreams.ErrOut = &notes
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--since", "0", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("old supported forward read failed: %v", err)
	}
	if !strings.Contains(out.String(), `"head": null`) || !strings.Contains(out.String(), `"seq": 2`) || !strings.Contains(notes.String(), "freshness comparison unavailable") {
		t.Fatalf("out=%s notes=%s", out.String(), notes.String())
	}
}

func TestTeamChatReadHeadFallbackDoesNotMaskUnrelatedRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"unrelated schema refusal", `{"errors":[{"message":"Unknown argument \"other\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, 2},
		{"business refusal naming beforeSeq", `{"errors":[{"message":"Unknown argument \"beforeSeq\".","extensions":{"code":"FORBIDDEN"}}]}`, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			teamGitDir(t)
			r := chatReadResponses()
			r["TeamChatReadHead"] = tc.body
			srv, calls := attnServer(t, r)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--json", "--server", srv.URL})
			if got := exitCodeFor(root.Execute()); got != tc.code {
				t.Fatalf("exit=%d want=%d", got, tc.code)
			}
			for _, c := range *calls {
				if c.Op == "TeamChatMessages" {
					t.Fatal("head refusal masked by fallback")
				}
			}
		})
	}
}
