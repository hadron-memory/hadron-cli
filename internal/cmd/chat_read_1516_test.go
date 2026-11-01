package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Distinct filters have independent offset streams, including on old providers
// that return platform messages in both streams. Clamp to two rows per page.
func chatReader1516(t *testing.T, mode, broken string) (*httptest.Server, func() map[string][]int) {
	t.Helper()
	legacy := chatMsg("chats:api:messages:t1-iris", 1, `{"author":"Iris","role":"Engineer","body":"academy"}`)
	platform := chatMsgCanonical("chats:api:messages:t2-rufus", 2, "platform", `{"authorName":"Rufus","sessionId":"s1","authorWorkerId":"w1"}`)
	record := chatMsg("chats:api:messages:t3-iris", 3, `{"author":"Iris","body":"record"}`)
	nilSeq := `{"node":{"loc":"chats:api:messages:t4-old","seq":null,"content":"no sequence","data":{"author":"Old"}}}`
	container := `{"node":{"loc":"chats:api:messages","seq":null,"data":null}}`
	rows := map[string][]string{"legacy": {legacy, platform, record, nilSeq, container}, "chat-message": {platform}}
	if mode == "new" {
		rows["legacy"] = []string{legacy, record, nilSeq, container}
	}
	if mode == "baseline" {
		rows["chat-message"] = nil
	}
	var mu sync.Mutex
	calls := map[string][]int{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			OperationName string
			Variables     struct {
				Filter struct {
					MemoryIds []string
					LocPrefix string
					NodeType  *string
				}
				Offset, Limit int
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		if q.OperationName != "ChatMessages" {
			t.Errorf("unexpected operation %s", q.OperationName)
			return
		}
		v := q.Variables
		if len(v.Filter.MemoryIds) != 1 || v.Filter.MemoryIds[0] != "hrn:mem:acme.com:tc" || v.Filter.LocPrefix != "chats:api:messages:" || v.Limit != 200 {
			t.Errorf("scope/page changed: %+v", v)
		}
		kind := "legacy"
		if v.Filter.NodeType != nil {
			kind = *v.Filter.NodeType
			if kind != "chat-message" {
				t.Errorf("unexpected nodeType %q", kind)
			}
		}
		mu.Lock()
		calls[kind] = append(calls[kind], v.Offset)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if broken == kind+"-error" {
			fmt.Fprint(w, gqlErrorJSON("FORBIDDEN"))
			return
		}
		page := rows[kind]
		offset := v.Offset
		if broken == kind {
			offset = 0
		}
		if offset >= len(page) {
			fmt.Fprint(w, chatMessagesResp())
			return
		}
		end := offset + 2
		if end > len(page) {
			end = len(page)
		}
		fmt.Fprint(w, chatMessagesResp(page[offset:end]...))
	}))
	t.Cleanup(s.Close)
	return s, func() map[string][]int {
		mu.Lock()
		defer mu.Unlock()
		result := map[string][]int{}
		for k, v := range calls {
			result[k] = append([]int{}, v...)
		}
		return result
	}
}

func executeChat1516(t *testing.T, server string, since int, asJSON bool) (string, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := []string{"chat", "read", "--node", "hrn:node:acme.com:tc:chats:api:messages", "--since", strconv.Itoa(since), "--server", server}
	if asJSON {
		args = append(args, "--json")
	}
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestChatReadDualDrain1516(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		for _, since := range []int{0, 1, 3} {
			name := fmt.Sprintf("since%d-json%v", since, asJSON)
			t.Run(name, func(t *testing.T) {
				path := filepath.Join("testdata", "chat-read-1516", name+".golden")
				if os.Getenv("UPDATE_1516_GOLDENS") == "1" {
					s, _ := chatReader1516(t, "baseline", "")
					out, err := executeChat1516(t, s.URL, since, asJSON)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(out), 0644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				for _, mode := range []string{"today", "new"} {
					t.Run(mode, func(t *testing.T) {
						s, calls := chatReader1516(t, mode, "")
						got, err := executeChat1516(t, s.URL, since, asJSON)
						if err != nil {
							t.Fatal(err)
						}
						if got != string(want) {
							t.Fatalf("changed transcript/JSON:\nwant %s\ngot %s", want, got)
						}
						c := calls()
						if len(c["legacy"]) < 2 || fmt.Sprint(c["chat-message"]) != "[0 1]" {
							t.Fatalf("did not drain independently: %v", c)
						}
					})
				}
			})
		}
	}
}

func TestChatReadEachDrainRefusesNonAdvancing1516(t *testing.T) {
	for _, kind := range []string{"legacy", "chat-message"} {
		t.Run(kind, func(t *testing.T) {
			s, calls := chatReader1516(t, "today", kind)
			out, err := executeChat1516(t, s.URL, 99, true)
			if err == nil || !strings.Contains(err.Error(), "not advancing") {
				t.Fatalf("ignored offset: %v calls=%v", err, calls())
			}
			if out != "" {
				t.Fatalf("partial report: %s", out)
			}
		})
	}
}

func TestChatReadEachDrainError1516(t *testing.T) {
	for _, kind := range []string{"legacy", "chat-message"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := chatReader1516(t, "today", kind+"-error")
			out, err := executeChat1516(t, s.URL, 0, true)
			if err == nil || exitCodeFor(err) != 8 || out != "" {
				t.Fatalf("read failure must preserve mapped refusal without partial report: %v %s", err, out)
			}
		})
	}
}
