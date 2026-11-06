package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func absentReferenceField1554(field string) string {
	return fmt.Sprintf(`{"errors":[{"message":%q,"extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, `Cannot query field "`+field+`" on type "Query".`)
}

// Literal legacy output pins reason/text omission and field projection, not page metadata.
const referenceRow1554 = `{"kind":"EDGE","field":"edge","sourceNodeId":"n1","sourceLoc":"msg:010:02","targetLoc":"msg:020:02","text":null,"reason":"PLACEHOLDER"}`

func runReferenceWire1554(t *testing.T, verb string, jsonMode bool, handler func(string, map[string]any) (string, int), extra ...string) (string, []string, error) {
	t.Helper()
	ops := []string{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
			Query         string         `json:"query"`
		}
		if e := json.NewDecoder(r.Body).Decode(&q); e != nil {
			t.Error(e)
			return
		}
		ops = append(ops, q.OperationName)
		if strings.HasPrefix(q.OperationName, "SpecBacklinks") || strings.HasPrefix(q.OperationName, "SpecUnresolvedReferences") {
			if !strings.Contains(q.Query, "sourceNodeId") || !strings.Contains(q.Query, "text") {
				t.Error("projection missing", q.Query)
			}
			if strings.HasSuffix(q.OperationName, "Page") && (q.Variables["limit"] != float64(20) || q.Variables["memoryRef"] == nil) {
				t.Error("page variables", q.Variables)
			}
			body, status := handler(q.OperationName, q.Variables)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			fmt.Fprint(w, body)
			return
		}
		if off, ok := q.Variables["offset"].(float64); ok && off > 0 && (q.OperationName == "FindNodes" || q.OperationName == "SpecPlaceholderScan") {
			fmt.Fprint(w, `{"data":{"findNodes":{"hits":[]},"nodes":[]}}`)
			return
		}
		data := map[string]string{"Memories": memListMicromentorJSON, "GetMemory": memGetVectorEnabledJSON, "SpecCorpusState": draftStateJSON, "SpecPlaceholderScan": placeholderScanJSON(map[string]bool{"msg:010:02": false}), "FindNodes": `{"data":{"findNodes":{"hits":[{"node":` + specNodeList("msg:010:02", `["spec"]`) + `}],"total":1}}}`, "NodeBatch": `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` + specBatchNode("msg:010:02") + `]}}}`}
		body, ok := data[q.OperationName]
		if !ok {
			t.Error("unexpected", q.OperationName)
			body = `{"data":{}}`
		}
		fmt.Fprint(w, body)
	}))
	defer s.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	a := []string{"spec", verb}
	if verb == "backlinks" {
		a = append(a, "msg:010:02")
	}
	if verb == "lint" {
		a = append(a, "--all")
	}
	a = append(a, "-m", specMem, "--server", s.URL)
	if jsonMode {
		a = append(a, "--json")
	}
	a = append(a, extra...)
	root.SetArgs(a)
	err := root.Execute()
	return out.String(), ops, err
}

func TestReferenceWireFailures1554(t *testing.T) {
	for _, verb := range []string{"backlinks", "unresolved", "lint"} {
		for _, jsonMode := range []bool{true, false} {
			for _, bad := range []string{
				`null`, `{}`, `{"items":null,"total":2,"hasMore":false}`, `{"items":[null],"total":2,"hasMore":false}`,
				`{"items":[],"total":2,"hasMore":true}`, `{"items":[],"total":2,"hasMore":false}`,
				`{"items":[],"total":3,"hasMore":false}`, `{"items":[],"total":-1,"hasMore":false}`,
				`{"items":[],"hasMore":false}`, `{"items":[],"total":2}`,
				`FORBIDDEN`, `NOT_FOUND`, `503`, `unknown`,
			} {
				t.Run(fmt.Sprintf("%s/json=%v/%s", verb, jsonMode, bad), func(t *testing.T) {
					pageCalls, legacyCalls := 0, 0
					out, _, err := runReferenceWire1554(t, verb, jsonMode, func(op string, v map[string]any) (string, int) {
						if !strings.HasSuffix(op, "Page") {
							legacyCalls++
							return `{"data":{}}`, 200
						}
						pageCalls++
						field := "specUnresolvedReferencesPage"
						if verb == "backlinks" {
							field = "specBacklinksPage"
						}
						if pageCalls == 1 {
							return `{"data":{"` + field + `":{"items":[` + referenceRow1554 + `],"total":2,"hasMore":true}}}`, 200
						}
						switch bad {
						case "FORBIDDEN", "NOT_FOUND":
							return fmt.Sprintf(`{"errors":[{"message":"later refused","extensions":{"code":%q}}]}`, bad), 200
						case "503":
							return "outage", 503
						case "unknown":
							return absentReferenceField1554(field), 400
						}
						return `{"data":{"` + field + `":` + bad + `}}`, 200
					})
					if err == nil || out != "" || pageCalls != 2 || legacyCalls != 0 {
						t.Fatalf("err=%v out=%q page=%d legacy=%d", err, out, pageCalls, legacyCalls)
					}
					if bad == "FORBIDDEN" && exitCodeFor(err) != exitcode.Forbidden {
						t.Fatal("mapped exit", err)
					}
					if bad == "NOT_FOUND" && exitCodeFor(err) != exitcode.NotFound {
						t.Fatal("mapped exit", err)
					}
				})
			}
		}
	}
}

func TestReferenceFallback1554(t *testing.T) {
	for _, verb := range []string{"backlinks", "unresolved", "lint"} {
		for _, jsonMode := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%v", verb, jsonMode), func(t *testing.T) {
				invoke := func(legacy bool) (string, []string, error) {
					return runReferenceWire1554(t, verb, jsonMode, func(op string, v map[string]any) (string, int) {
						field := "specUnresolvedReferences"
						if verb == "backlinks" {
							field = "specBacklinks"
						}
						if strings.HasSuffix(op, "Page") {
							if legacy {
								return absentReferenceField1554(field + "Page"), 400
							}
							return `{"data":{"` + field + `Page":{"items":[` + referenceRow1554 + `],"total":1,"hasMore":false}}}`, 200
						}
						return `{"data":{"` + field + `":[` + referenceRow1554 + `]}}`, 200
					})
				}
				modern, _, err := invoke(false)
				if err != nil {
					t.Fatal(err)
				}
				mode := "text"
				if jsonMode {
					mode = "json"
				}
				baseline, e := os.ReadFile("testdata/spec_reference_" + verb + "_" + mode + "_base.golden")
				if e != nil {
					t.Fatal(e)
				}
				if modern != string(baseline) {
					t.Fatalf("output changed from main3edded21: got=%q want=%q", modern, baseline)
				}

				old, ops, err := invoke(true)
				if err != nil || old != modern {
					t.Fatalf("modern=%q legacy=%q err=%v", modern, old, err)
				}
				refs := []string{}
				for _, op := range ops {
					if strings.HasPrefix(op, "SpecBacklinks") || strings.HasPrefix(op, "SpecUnresolvedReferences") {
						refs = append(refs, op)
					}
				}
				if len(refs) != 2 || !strings.HasSuffix(refs[0], "Page") || strings.HasSuffix(refs[1], "Page") {
					t.Fatal(refs)
				}
			})
		}
	}
	for _, code := range []string{"FORBIDDEN", "INTERNAL_SERVER_ERROR", "GRAPHQL_VALIDATION_FAILED"} {
		t.Run("no prose fallback/"+code, func(t *testing.T) {
			calls := 0
			out, _, err := runReferenceWire1554(t, "unresolved", true, func(op string, v map[string]any) (string, int) {
				calls++
				return fmt.Sprintf(`{"errors":[{"message":"bad specUnresolvedReferencesPage query","extensions":{"code":%q}}]}`, code), 200
			})
			if err == nil || out != "" || calls != 1 {
				t.Fatalf("out=%s err=%v calls=%d", out, err, calls)
			}
		})
	}
}

func TestReferenceFirstPageRefusalNeverFallback1554(t *testing.T) {
	unknown := absentReferenceField1554("specUnresolvedReferencesPage")
	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"permission exact prose", strings.Replace(unknown, "GRAPHQL_VALIDATION_FAILED", "FORBIDDEN", 1), 400},
		{"500 exact validation", unknown, 500},
		{"unrelated field", absentReferenceField1554("other"), 400},
		{"legacy name from page is unrelated", absentReferenceField1554("specUnresolvedReferences"), 400},
		{"mixed", strings.Replace(unknown, `}]}`, `},{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`, 1), 400},
		{"null error entry", strings.Replace(unknown, `}]}`, `},null]}`, 1), 200},
		{"populated partial", strings.Replace(unknown, `{"errors":`, `{"data":{"specUnresolvedReferencesPage":{"items":[],"total":0,"hasMore":false}},"errors":`, 1), 200},
		{"http data partial", strings.Replace(unknown, `{"errors":`, `{"data":{"specUnresolvedReferencesPage":null},"errors":`, 1), 400},
		{"missing page", `{"data":{}}`, 200},
		{"null item with zero total", `{"data":{"specUnresolvedReferencesPage":{"items":[null],"total":0,"hasMore":false}}}`, 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			out, _, err := runReferenceWire1554(t, "lint", true, func(op string, v map[string]any) (string, int) { calls++; return tt.body, tt.status })
			if err == nil || out != "" || calls != 1 {
				t.Fatalf("out=%s err=%v calls=%d", out, err, calls)
			}
		})
	}
}

func TestReferenceDraftScopeAndLegacyAbsence1554(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(fmt.Sprint(strict), func(t *testing.T) {
			extra := []string{}
			if strict {
				extra = append(extra, "--strict")
			}
			calls := 0
			out, _, err := runReferenceWire1554(t, "lint", true, func(op string, v map[string]any) (string, int) {
				calls++
				if !strings.HasSuffix(op, "Page") {
					t.Error("legacy", op)
				}
				row := referenceRow1554
				more := false
				if calls == 1 {
					row = strings.Replace(row, "msg:010:02", "zzz:out:of:scope", 1)
					more = true
				}
				return fmt.Sprintf(`{"data":{"specUnresolvedReferencesPage":{"items":[%s],"total":2,"hasMore":%v}}}`, row, more), 200
			}, extra...)
			if strict {
				if exitCodeFor(err) != exitcode.Conflict {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var got []map[string]any
			if e := json.Unmarshal([]byte(out), &got); e != nil {
				t.Fatal(e)
			}
			n := 0
			for _, r := range got {
				if r["rule"] == "unresolved-reference" {
					n++
					if r["citation"] != "msg:010:02" {
						t.Fatal(r)
					}
					sev := "warning"
					if strict {
						sev = "error"
					}
					if r["severity"] != sev {
						t.Fatal(r)
					}
				}
			}
			if n != 1 || calls != 2 {
				t.Fatalf("findings=%s calls=%d", out, calls)
			}
		})
	}
	calls := 0
	out, _, err := runReferenceWire1554(t, "lint", true, func(op string, v map[string]any) (string, int) {
		calls++
		if strings.HasSuffix(op, "Page") {
			return absentReferenceField1554("specUnresolvedReferencesPage"), 400
		}
		return absentReferenceField1554("specUnresolvedReferences"), 400
	})
	if err != nil || calls != 2 || strings.Contains(out, "unresolved-reference") {
		t.Fatalf("ordinary-rule compatibility out=%s err=%v calls=%d", out, err, calls)
	}
}

func TestReferenceRequiredItemFields1554(t *testing.T) {
	for _, verb := range []string{"backlinks", "unresolved", "lint"} {
		for _, legacy := range []bool{false, true} {
			for _, field := range []string{"kind", "field", "sourceLoc", "sourceNodeId", "targetLoc"} {
				for _, missing := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/legacy=%v/%s/missing=%v", verb, legacy, field, missing), func(t *testing.T) {
						row := map[string]any{}
						if err := json.Unmarshal([]byte(referenceRow1554), &row); err != nil {
							t.Fatal(err)
						}
						if missing {
							delete(row, field)
						} else {
							row[field] = nil
						}
						encoded, err := json.Marshal(row)
						if err != nil {
							t.Fatal(err)
						}
						out, _, err := runReferenceWire1554(t, verb, true, func(op string, v map[string]any) (string, int) {
							root := "specUnresolvedReferences"
							if verb == "backlinks" {
								root = "specBacklinks"
							}
							if strings.HasSuffix(op, "Page") {
								if legacy {
									return absentReferenceField1554(root + "Page"), 400
								}
								return `{"data":{"` + root + `Page":{"items":[` + string(encoded) + `],"total":1,"hasMore":false}}}`, 200
							}
							return `{"data":{"` + root + `":[` + string(encoded) + `]}}`, 200
						})
						if err == nil || out != "" {
							t.Fatalf("malformed item must fail whole read: out=%s err=%v", out, err)
						}
					})
				}
			}
		}
	}
}

func TestReferenceEmptyStringsRemainValues1554(t *testing.T) {
	for _, verb := range []string{"backlinks", "unresolved"} {
		out, _, err := runReferenceWire1554(t, verb, true, func(op string, v map[string]any) (string, int) {
			field := "specUnresolvedReferencesPage"
			if verb == "backlinks" {
				field = "specBacklinksPage"
			}
			return `{"data":{"` + field + `":{"items":[{"kind":"URN","field":"","sourceNodeId":"","sourceLoc":"","targetLoc":"","text":null,"reason":null}],"total":1,"hasMore":false}}}`, 200
		})
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			References []map[string]any `json:"references"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.References) != 1 || got.References[0]["sourceLoc"] != "" {
			t.Fatal(out)
		}
	}
}

func TestReferenceTypedPartialErrorWins1554(t *testing.T) {
	for _, verb := range []string{"backlinks", "unresolved", "lint"} {
		for _, legacy := range []bool{false, true} {
			for _, code := range []string{"FORBIDDEN", "NOT_FOUND"} {
				t.Run(fmt.Sprintf("%s/legacy=%v/%s", verb, legacy, code), func(t *testing.T) {
					calls := 0
					out, _, err := runReferenceWire1554(t, verb, true, func(op string, v map[string]any) (string, int) {
						calls++
						field := "specUnresolvedReferences"
						if verb == "backlinks" {
							field = "specBacklinks"
						}
						if calls == 1 {
							if legacy {
								return absentReferenceField1554(field + "Page"), 400
							}
							return `{"data":{"` + field + `Page":{"items":[` + referenceRow1554 + `],"total":2,"hasMore":true}}}`, 200
						}
						payload := `{"` + field + `Page":{"items":[null],"total":2,"hasMore":false}}`
						if legacy {
							payload = `{"` + field + `":[null]}`
						}
						return `{"data":` + payload + `,"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}]}`, 200
					})
					want := exitcode.Forbidden
					if code == "NOT_FOUND" {
						want = exitcode.NotFound
					}
					if exitCodeFor(err) != want || out != "" || calls != 2 {
						t.Fatalf("out=%q err=%v code=%d want=%d calls=%d", out, err, exitCodeFor(err), want, calls)
					}
				})
			}
		}
	}
}
