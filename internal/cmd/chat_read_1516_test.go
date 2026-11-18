package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// baseline1516 is the main revision the committed goldens record: the chat
// reader's behaviour BEFORE the dual-drain change (#1516), captured on
// cbe7dd32 and replayed independently. They are a baseline, not an expected
// value to keep current: regenerating them from newer code would replace the
// baseline with the current behaviour and make every comparison vacuous
// (cli#1519). Regeneration is therefore refused unless the request names this
// revision AND the code under test is byte-identical to it.
const baseline1516 = "cbe7dd3203a86ccbac893e49c14ca2082b6d19a9"

// baselineCodePathspecs1516 define "the code under test": the whole production
// tree, not a guess at the reader's dependency closure. The goldens are produced
// through the real command tree (package cmd drives chat read), so command
// wiring, rendering, --since handling and the API layer all count; only test
// files and testdata may differ, as they did when the goldens were first
// recorded (the test commit sits on top of the baseline revision). ":(top)"
// anchors each path at the repository root.
var baselineCodePathspecs1516 = []string{
	":(top)internal", ":(top)cmd", ":(top)go.mod", ":(top)go.sum",
	":(top,exclude,glob)**/*_test.go", ":(top,exclude)**/testdata/**",
}

// guardGoldenUpdate1516 decides whether a golden update may write. codeDiffers
// says the code under test differs from the baseline's (or could not be
// compared, which refuses too).
func guardGoldenUpdate1516(requested string, codeDiffers bool, detail string) error {
	if requested != baseline1516 {
		return fmt.Errorf("UPDATE_1516_GOLDENS must name the full pinned baseline revision %s (got %q): a bare flag would let any checkout replace the baseline", baseline1516, requested)
	}
	if codeDiffers {
		return fmt.Errorf("this checkout is not the pinned baseline: its production code (internal/, cmd/, go.mod, go.sum; test files and testdata excluded) differs from %s, so regenerating would record current behaviour as the baseline (%s)", baseline1516, detail)
	}
	return nil
}

// checkoutDiffersFromBaseline1516 compares the production code with the
// baseline revision using the repository this test file lives in.
func checkoutDiffersFromBaseline1516() (bool, string) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return true, "cannot locate the test source"
	}
	return compareWithBaseline1516(filepath.Dir(file), baseline1516, baselineCodePathspecs1516)
}

// compareWithBaseline1516 reports whether the WORKING TREE under dir differs
// from the revision, over pathspecs. It compares the tree itself, so uncommitted
// edits to tracked files count, and it also counts untracked files (a new file
// can change behaviour without ever being in a diff). Anything it cannot
// establish (no git, the revision missing from a shallow clone, a failed
// command) counts as "differs": the guard fails closed. The detail names the
// checkout's current HEAD so a refusal diagnoses itself.
func compareWithBaseline1516(dir, revision string, pathspecs []string) (bool, string) {
	head := "unknown HEAD"
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output(); err == nil {
		head = "HEAD " + strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", revision+"^{commit}").CombinedOutput(); err != nil {
		return true, head + "; baseline revision not available here: " + strings.TrimSpace(string(out))
	}
	diff := append([]string{"-C", dir, "diff", "--quiet", revision, "--"}, pathspecs...)
	if err := exec.Command("git", diff...).Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return true, head + "; the working tree differs from the baseline"
		}
		return true, head + "; git diff failed: " + err.Error()
	}
	untracked := append([]string{"-C", dir, "ls-files", "--others", "--exclude-standard", "--"}, pathspecs...)
	out, err := exec.Command("git", untracked...).Output()
	if err != nil {
		return true, head + "; git ls-files failed: " + err.Error()
	}
	if strings.TrimSpace(string(out)) != "" {
		return true, head + "; untracked files under the production tree"
	}
	return false, ""
}

// provenance1516 renders the PROVENANCE file: the pinned revision and the
// SHA-256 of every golden, so replacing a golden is a visible, reviewable
// change to a file that names the revision it was recorded from.
func provenance1516(revision string, goldens map[string][]byte) string {
	names := make([]string, 0, len(goldens))
	for name := range goldens {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "# cli#1519: the recorded baseline of the chat read goldens (chat_read_1516_test.go).\n")
	fmt.Fprintf(&b, "# Regenerate only from a checkout whose production code (internal/, cmd/, go.mod, go.sum; not *_test.go or testdata) equals the revision below, with a clean tree:\n")
	fmt.Fprintf(&b, "#   UPDATE_1516_GOLDENS=<revision> go test ./internal/cmd -run TestChatReadDualDrain1516\n")
	fmt.Fprintf(&b, "revision %s\n", revision)
	for _, name := range names {
		sum := sha256.Sum256(goldens[name])
		fmt.Fprintf(&b, "sha256 %s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	return b.String()
}

func parseProvenance1516(text string) (revision string, sums map[string]string, err error) {
	sums = map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "revision "):
			revision = strings.TrimPrefix(line, "revision ")
		case strings.HasPrefix(line, "sha256 "):
			f := strings.Fields(strings.TrimPrefix(line, "sha256 "))
			if len(f) != 2 {
				return "", nil, fmt.Errorf("bad sum line %q", line)
			}
			sums[f[1]] = f[0]
		default:
			return "", nil, fmt.Errorf("unrecognised line %q", line)
		}
	}
	if revision == "" {
		return "", nil, errors.New("no revision line")
	}
	return revision, sums, nil
}

func readGoldens1516(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	goldens := map[string][]byte{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".golden") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		goldens[e.Name()] = b
	}
	return goldens, nil
}

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
	return chatRowsReader1516(t, rows, broken)
}

// chatRowsReader1516 serves the given rows per stream ("legacy" is the
// unqualified drain, "chat-message" the explicit one), clamping every page to
// two rows, and records each stream's requested offsets (cli#1519 reuses it
// for fixtures the three modes above cannot express).
func chatRowsReader1516(t *testing.T, rows map[string][]string, broken string) (*httptest.Server, func() map[string][]int) {
	t.Helper()
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
		tooMany := broken == kind && len(calls[kind]) > 4
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if broken == kind+"-error" || tooMany {
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
				if request := os.Getenv("UPDATE_1516_GOLDENS"); request != "" {
					differs, detail := checkoutDiffersFromBaseline1516()
					if err := guardGoldenUpdate1516(request, differs, detail); err != nil {
						t.Fatalf("refusing to regenerate the 1516 goldens: %v", err)
					}
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
					// The output names the revision it was recorded from.
					goldens, err := readGoldens1516(filepath.Dir(path))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(filepath.Dir(path), "PROVENANCE"), []byte(provenance1516(request, goldens)), 0644); err != nil {
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
						// cli#1519: the exact offset sequence of each drain, so a
						// skipped, repeated or drifted offset in EITHER stream
						// fails. Both drains end on their empty page: today's
						// five-row unqualified stream asks 0,2,4 and then 5; the
						// post-exclusion four-row stream asks 0,2,4; the explicit
						// stream's one row asks 0 and 1.
						c := calls()
						wantLegacy, known := map[string]string{"today": "[0 2 4 5]", "new": "[0 2 4]"}[mode]
						if !known {
							t.Fatalf("no expected offsets for mode %q: add them", mode)
						}
						if fmt.Sprint(c["legacy"]) != wantLegacy || fmt.Sprint(c["chat-message"]) != "[0 1]" {
							t.Fatalf("did not drain independently: legacy %v (want %s) chat-message %v (want [0 1])", c["legacy"], wantLegacy, c["chat-message"])
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
