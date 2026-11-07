package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#1234: --unlock supplies an encrypted memory's passphrase for ONE
// invocation, in the x-hadron-memory-keys header (cor:sec:040:04, ruling 0129).

const unlockHeader1234 = "X-Hadron-Memory-Keys"

// A sentinel with JSON-significant characters, so an echo of the header's
// escaped spelling is caught as well as the raw value.
const sentinel1234 = `S3NT1NEL-pass"phr\ase`

type unlockCall1234 struct {
	op     string
	header string
}

// unlockServer1234 answers by operation name and records every request's
// unlock header. reply may inspect the header to decide what it returns.
func unlockServer1234(t *testing.T, reply func(op, header string) (int, string)) (*httptest.Server, *[]unlockCall1234) {
	t.Helper()
	calls := []unlockCall1234{}
	var mu sync.Mutex // independent reads may arrive together (cli#1677)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		h := r.Header.Get(unlockHeader1234)
		mu.Lock()
		calls = append(calls, unlockCall1234{op: body.OperationName, header: h})
		mu.Unlock()
		status, resp := reply(body.OperationName, h)
		if def, ok := unstubbedDefault(body.OperationName); ok && status == 200 && strings.Contains(resp, "unexpected "+body.OperationName) {
			resp = def
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func passFile1234(t *testing.T, pass string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(p, []byte(pass+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func memoriesBody1234() string {
	return `{"data":{"memories":{"total":0,"items":[]}}}`
}

func run1234(t *testing.T, f *cmdutil.Factory, srv *httptest.Server, args ...string) error {
	t.Helper()
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", srv.URL))
	return root.Execute()
}

func TestUnlock1234HeaderCarriesCanonicalMemoryAndPassphrase(t *testing.T) {
	srv, calls := unlockServer1234(t, func(string, string) (int, string) { return 200, memoriesBody1234() })
	f, _ := testFactory(t)
	if err := run1234(t, f, srv, "memory", "ls", "--json", "--unlock", "acme.com:diary",
		"--unlock-passphrase-file", passFile1234(t, "pw one", 0o600)); err != nil {
		t.Fatal(err)
	}
	if len(*calls) == 0 {
		t.Fatal("no request")
	}
	for _, c := range *calls {
		var entries []map[string]string
		if err := json.Unmarshal([]byte(c.header), &entries); err != nil {
			t.Fatalf("%s header %q: %v", c.op, c.header, err)
		}
		if len(entries) != 1 || entries[0]["memory"] != "hrn:mem:acme.com:diary" || entries[0]["passphrase"] != "pw one" || len(entries[0]) != 2 {
			t.Fatalf("%s entries %+v", c.op, entries)
		}
	}
}

func TestUnlock1234NoFlagSendsNoHeader(t *testing.T) {
	srv, calls := unlockServer1234(t, func(string, string) (int, string) { return 200, memoriesBody1234() })
	f, _ := testFactory(t)
	if err := run1234(t, f, srv, "memory", "ls", "--json"); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c.header != "" {
			t.Fatalf("%s sent %q", c.op, c.header)
		}
	}
}

func TestUnlock1234NodeGetReadsBackPlaintext(t *testing.T) {
	for _, unlock := range []bool{false, true} {
		srv, _ := unlockServer1234(t, func(op, header string) (int, string) {
			content := `"enc:v1:Y2lwaGVy"`
			if strings.Contains(header, "right pass") {
				content = `"dear diary"`
			}
			switch op {
			case "ResolveUrn":
				return 200, `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`
			case "GetNode":
				return 200, strings.Replace(node1513("null"), `"content":null`, `"content":`+content, 1)
			}
			return 200, `{"data":null,"errors":[{"message":"unexpected ` + op + `"}]}`
		})
		f, out := testFactory(t)
		args := []string{"node", "get", testNodeURN}
		if unlock {
			args = append(args, "--unlock", "acme.com:kb", "--unlock-passphrase-file", passFile1234(t, "right pass", 0o600))
		}
		if err := run1234(t, f, srv, args...); err != nil {
			t.Fatal(err)
		}
		if unlock != strings.Contains(out.String(), "dear diary") || unlock == strings.Contains(out.String(), "enc:v1:") {
			t.Fatalf("unlock=%v:\n%s", unlock, out.String())
		}
	}
}

// Every refusal below is decided before any request is sent.
func TestUnlock1234LocalRefusalsSendNothing(t *testing.T) {
	nine := []string{}
	for _, m := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		nine = append(nine, "--unlock", "acme.com:"+m)
	}
	cases := map[string]struct {
		args  []string
		stdin string
	}{
		"group-readable file":   {args: []string{"--unlock", "acme.com:kb", "--unlock-passphrase-file", "MODE0640"}},
		"other-readable file":   {args: []string{"--unlock", "acme.com:kb", "--unlock-passphrase-file", "MODE0604"}},
		"ninth unlock":          {args: append(append([]string{}, nine...), "--unlock-passphrase-stdin"), stdin: "pw\n"},
		"stdin with two":        {args: []string{"--unlock", "acme.com:a", "--unlock", "acme.com:b", "--unlock-passphrase-stdin"}, stdin: "pw\n"},
		"stdin and file":        {args: []string{"--unlock", "acme.com:a", "--unlock-passphrase-stdin", "--unlock-passphrase-file", "MODE0600"}, stdin: "pw\n"},
		"file count mismatch":   {args: []string{"--unlock", "acme.com:a", "--unlock", "acme.com:b", "--unlock-passphrase-file", "MODE0600"}},
		"duplicate memory":      {args: []string{"--unlock", "acme.com:a", "--unlock", "acme.com::a", "--unlock-passphrase-file", "MODE0600", "--unlock-passphrase-file", "MODE0600"}},
		"empty memory":          {args: []string{"--unlock", " ", "--unlock-passphrase-stdin"}, stdin: "pw\n"},
		"no source, no tty":     {args: []string{"--unlock", "acme.com:kb"}},
		"source without unlock": {args: []string{"--unlock-passphrase-stdin"}, stdin: "pw\n"},
		"empty stdin":           {args: []string{"--unlock", "acme.com:kb", "--unlock-passphrase-stdin"}, stdin: "\n"},
		"empty file":            {args: []string{"--unlock", "acme.com:kb", "--unlock-passphrase-file", "EMPTY"}},
		"missing file":          {args: []string{"--unlock", "acme.com:kb", "--unlock-passphrase-file", "/nonexistent/1234/pass"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv, calls := unlockServer1234(t, func(string, string) (int, string) { return 200, memoriesBody1234() })
			f, _ := testFactory(t)
			f.IOStreams.In = strings.NewReader(c.stdin)
			args := []string{"memory", "ls"}
			for _, a := range c.args {
				switch a {
				case "MODE0640":
					a = passFile1234(t, "pw", 0o640)
				case "MODE0604":
					a = passFile1234(t, "pw", 0o604)
				case "MODE0600":
					a = passFile1234(t, "pw", 0o600)
				case "EMPTY":
					a = passFile1234(t, "", 0o600)
				}
				args = append(args, a)
			}
			err := run1234(t, f, srv, args...)
			if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
				t.Fatalf("err=%v calls=%+v", err, *calls)
			}
		})
	}
}

func TestUnlock1234StdinOneMemory(t *testing.T) {
	srv, calls := unlockServer1234(t, func(string, string) (int, string) { return 200, memoriesBody1234() })
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("from stdin\r\n")
	if err := run1234(t, f, srv, "memory", "ls", "--json", "--unlock", "acme.com:kb", "--unlock-passphrase-stdin"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) == 0 || !strings.Contains((*calls)[0].header, `"passphrase":"from stdin"`) {
		t.Fatalf("%+v", *calls)
	}
}

func TestUnlock1234FilesPairInOrder(t *testing.T) {
	srv, calls := unlockServer1234(t, func(string, string) (int, string) { return 200, memoriesBody1234() })
	f, _ := testFactory(t)
	if err := run1234(t, f, srv, "memory", "ls", "--json",
		"--unlock", "acme.com:a", "--unlock-passphrase-file", passFile1234(t, "pa", 0o600),
		"--unlock", "acme.com:b", "--unlock-passphrase-file", passFile1234(t, "pb", 0o400)); err != nil {
		t.Fatal(err)
	}
	want := `[{"memory":"hrn:mem:acme.com:a","passphrase":"pa"},{"memory":"hrn:mem:acme.com:b","passphrase":"pb"}]`
	if len(*calls) == 0 || (*calls)[0].header != want {
		t.Fatalf("got %+v want %s", *calls, want)
	}
}

func TestUnlock1234TTYPromptPerMemoryNoEcho(t *testing.T) {
	srv, calls := unlockServer1234(t, func(string, string) (int, string) { return 200, memoriesBody1234() })
	f, _, errOut := testFactoryTTY(t, "")
	labels := []string{}
	restore := cmdutil.SetUnlockPromptForTest(func(_ *output.IOStreams, label string) (string, error) {
		labels = append(labels, label)
		return "typed-" + label[len(label)-1:], nil
	})
	defer restore()
	if err := run1234(t, f, srv, "memory", "ls", "--json", "--unlock", "acme.com:a", "--unlock", "acme.com:b"); err != nil {
		t.Fatal(err)
	}
	if len(labels) != 2 || !strings.Contains(labels[0], "hrn:mem:acme.com:a") || !strings.Contains(labels[1], "hrn:mem:acme.com:b") {
		t.Fatalf("labels %q", labels)
	}
	if len(*calls) == 0 || !strings.Contains((*calls)[0].header, "typed-") {
		t.Fatalf("%+v", *calls)
	}
	if strings.Contains(errOut.String(), "typed-") {
		t.Fatalf("passphrase echoed: %s", errOut.String())
	}
}

// The five unlock codes arrive as an Apollo context failure (HTTP 500 with an
// errors envelope, no path) before any resolver runs.
func TestUnlock1234FiveCodesAreTyped(t *testing.T) {
	want := map[string]int{
		"WRONG_KEY":        exitcode.Usage,
		"BAD_UNLOCK_INPUT": exitcode.Usage,
		"NOT_ENCRYPTED":    exitcode.Usage,
		"NOT_FOUND":        exitcode.NotFound,
		"UNAUTHENTICATED":  exitcode.AuthRequired,
	}
	for code, exit := range want {
		t.Run(code, func(t *testing.T) {
			srv, calls := unlockServer1234(t, func(string, string) (int, string) {
				return 500, `{"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}]}`
			})
			f, out := testFactory(t)
			err := run1234(t, f, srv, "memory", "ls", "--json", "--unlock", "acme.com:kb",
				"--unlock-passphrase-file", passFile1234(t, "pw", 0o600))
			if err == nil || renderError(f, err) != exit {
				t.Fatalf("err=%v", err)
			}
			var env struct {
				Error struct {
					Code       int    `json:"code"`
					Message    string `json:"message"`
					ServerCode string `json:"serverCode"`
				} `json:"error"`
			}
			if jerr := json.Unmarshal([]byte(out.String()), &env); jerr != nil {
				t.Fatal(jerr, out.String())
			}
			if env.Error.Code != exit || env.Error.ServerCode != code || !strings.Contains(env.Error.Message, "--unlock") || !strings.Contains(env.Error.Message, code) {
				t.Fatalf("envelope %+v", env.Error)
			}
			if len(*calls) != 1 {
				t.Fatalf("an unlock refusal must not be retried: %+v", *calls)
			}
		})
	}
}

// A hostile or echoing server puts the header and the passphrase into its
// error; neither reaches stdout or stderr, in any rendering.
func TestUnlock1234SentinelNeverPrinted(t *testing.T) {
	for _, mode := range []string{"wrong-key", "echo-200", "echo-200-plain", "echo-500-html", "success"} {
		for _, json := range []bool{false, true} {
			t.Run(mode+map[bool]string{true: "-json", false: ""}[json], func(t *testing.T) {
				// The header is JSON, so it carries the sentinel only in its
				// JSON-escaped spelling (the sentinel has a quote and a
				// backslash): echoing the header alone never puts the raw
				// passphrase in a reply. Echo both, and require neither printed
				// (PR #1616 LOCAL P3-1: without that, a no-redactor mutant survived).
				var seenHeader string
				srv, _ := unlockServer1234(t, func(op, header string) (int, string) {
					seenHeader = header
					esc, _ := jsonString1234(header)
					pass, _ := jsonString1234(sentinel1234)
					switch mode {
					case "wrong-key":
						return 500, `{"errors":[{"message":"bad ` + esc + ` ` + pass + `","extensions":{"code":"WRONG_KEY","header":` + mustJSON1234(header) + `}}]}`
					case "echo-200":
						return 200, `{"data":null,"errors":[{"message":"echo ` + esc + ` ` + pass + `","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
					case "echo-200-plain":
						// A code-less resolver error is rendered with its message.
						return 200, `{"data":null,"errors":[{"message":"echo ` + esc + ` ` + pass + `","path":["memories"]}]}`
					case "echo-500-html":
						return 502, `<html>upstream saw ` + header + ` ` + sentinel1234 + `</html>`
					}
					return 200, memoriesBody1234()
				})
				f, out := testFactory(t)
				args := []string{"memory", "ls", "--unlock", "acme.com:kb", "--unlock-passphrase-file", passFile1234(t, sentinel1234, 0o600)}
				if json {
					args = append(args, "--json")
				}
				err := run1234(t, f, srv, args...)
				if err != nil {
					renderError(f, err)
				}
				all := out.String() + f.IOStreams.ErrOut.(*strings.Builder).String()
				if err != nil {
					all += err.Error()
				}
				esc, _ := jsonString1234(sentinel1234)
				if mode != "success" && seenHeader == "" {
					t.Fatal("the server saw no --unlock header")
				}
				leaks := []string{sentinel1234, esc, "S3NT1NEL"}
				if seenHeader != "" {
					leaks = append(leaks, seenHeader)
				}
				for _, s := range leaks {
					if strings.Contains(all, s) {
						t.Fatalf("leaked %q in:\n%s", s, all)
					}
				}
			})
		}
	}
}

func jsonString1234(s string) (string, error) {
	b, err := json.Marshal(s)
	return strings.Trim(string(b), `"`), err
}

func mustJSON1234(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Ruling 0129: never argv, never env. There is no flag that takes the
// passphrase itself, and no environment variable is read.
func TestUnlock1234NoArgvOrEnvSource(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	for _, name := range []string{"passphrase", "unlock-passphrase", "unlock-key", "data-key"} {
		if root.PersistentFlags().Lookup(name) != nil {
			t.Fatalf("root has a --%s flag", name)
		}
	}
	for _, name := range []string{"unlock", "unlock-passphrase-file", "unlock-passphrase-stdin"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Fatalf("root lacks --%s", name)
		}
	}
	for _, env := range []string{"HADRON_UNLOCK_PASSPHRASE", "HADRON_PASSPHRASE", "HADRON_MEMORY_PASSPHRASE", "HADRON_UNLOCK"} {
		t.Setenv(env, "from-env")
	}
	srv, calls := unlockServer1234(t, func(string, string) (int, string) { return 200, memoriesBody1234() })
	err := run1234(t, f, srv, "memory", "ls", "--unlock", "acme.com:kb")
	if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
		t.Fatalf("an env passphrase was used: err=%v calls=%+v", err, *calls)
	}
}

func TestUnlock1234HelpMentionsUnlock(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	enc, _, err := root.Find([]string{"memory", "encrypt"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc.Long, "cannot read the content any more") || !strings.Contains(enc.Long+enc.Example, "--unlock") {
		t.Fatalf("memory encrypt help:\n%s\n%s", enc.Long, enc.Example)
	}
	for _, path := range [][]string{{"node", "get"}, {"search"}} {
		c, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(c.Long, "--unlock") {
			t.Fatalf("%v help lacks --unlock", path)
		}
	}
}

// hadron-cli#1620 (PR #1616 §10 M08): an --unlock refusal renders the fixed
// sentence for its code and never the server's own message, at either call
// site (Apollo's 500 and a path-less error in a 200 envelope), in either
// rendering. The marker is deliberately not a secret, so the redactor leaves
// it alone and only unlockRefusal's "never the server's text" keeps it out.
func TestUnlock1234RefusalNeverRendersServerMessage(t *testing.T) {
	sentences := map[string]string{
		"WRONG_KEY": "a passphrase does not match its memory",
		"NOT_FOUND": "an --unlock memory was not found",
	}
	const marker = "server-said-XYZ"
	for code, sentence := range sentences {
		for _, status := range []int{500, 200} {
			for _, asJSON := range []bool{false, true} {
				name := code + "-" + http.StatusText(status) + map[bool]string{true: "-json", false: ""}[asJSON]
				t.Run(name, func(t *testing.T) {
					srv, _ := unlockServer1234(t, func(string, string) (int, string) {
						return status, `{"data":null,"errors":[{"message":"` + marker + ` ` + code + `","extensions":{"code":"` + code + `"}}]}`
					})
					f, out := testFactory(t)
					args := []string{"memory", "ls", "--unlock", "acme.com:kb", "--unlock-passphrase-file", passFile1234(t, "pw", 0o600)}
					if asJSON {
						args = append(args, "--json")
					}
					err := run1234(t, f, srv, args...)
					if err == nil {
						t.Fatal("want a refusal")
					}
					renderError(f, err)
					all := out.String() + f.IOStreams.ErrOut.(*strings.Builder).String() + err.Error()
					if strings.Contains(all, marker) {
						t.Fatalf("the server's message was rendered:\n%s", all)
					}
					if !strings.Contains(all, sentence) || !strings.Contains(all, "--unlock refused ("+code+")") {
						t.Fatalf("want the fixed %s sentence, got:\n%s", code, all)
					}
					if asJSON {
						var env struct {
							Error struct {
								Message    string `json:"message"`
								ServerCode string `json:"serverCode"`
							} `json:"error"`
						}
						if jerr := json.Unmarshal([]byte(out.String()), &env); jerr != nil {
							t.Fatal(jerr, out.String())
						}
						if env.Error.ServerCode != code || !strings.Contains(env.Error.Message, sentence) || strings.Contains(env.Error.Message, marker) {
							t.Fatalf("envelope %+v", env.Error)
						}
					}
				})
			}
		}
	}
}
