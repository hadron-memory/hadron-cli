package cmd

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const tickerFixture = `{"id":"cticker000000000000000000","urn":null,"name":"daily","slug":"daily","app":{"id":"app1"},"cadenceSeconds":600,"anchorAt":"2026-10-19T00:00:00Z","quietHours":null,"channels":[],"relevance":"ALL","reasons":["READ_TEAM_CHANNEL","EVERY_TICK"],"enabled":true,"owner":null,"nextTickAt":null,"lastTickAt":null,"createdAt":"2026-10-19T00:00:00Z","updatedAt":null}`

func TestTickerCreateReasonsWire(t *testing.T) {
	for _, reasons := range []string{"", "EVERY_TICK,read-team-channel,every-tick"} {
		t.Run(reasons, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{"CreateTicker": `{"data":{"createTicker":` + tickerFixture + `}}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"ticker", "create", "--app", "acme.com:ops", "--name", "daily", "--cadence-seconds", "600", "--json", "--server", gql.URL}
			if reasons != "" {
				args = append(args, "--reasons", reasons)
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var v struct{ Input map[string]any }
			if err := json.Unmarshal(captured["CreateTicker"], &v); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"enabled", "anchorAt", "quietHours", "channelIds", "relevance"} {
				if _, ok := v.Input[key]; ok {
					t.Errorf("omitted %s sent: %s", key, captured["CreateTicker"])
				}
			}
			if reasons == "" {
				if _, ok := v.Input["reasons"]; ok {
					t.Fatal("default must be server-owned")
				}
			} else if got, _ := json.Marshal(v.Input["reasons"]); string(got) != `["READ_TEAM_CHANNEL","EVERY_TICK"]` {
				t.Fatalf("reasons %s", got)
			}
			if !strings.Contains(out.String(), `"urn": null`) || !strings.Contains(out.String(), `"every-tick"`) {
				t.Fatal(out.String())
			}
		})
	}
}

func TestTickerRejectsInvalidInputsBeforeNetwork(t *testing.T) {
	for _, args := range [][]string{
		{"create", "--name", "n", "--cadence-seconds", "600", "--reasons="},
		{"create", "--name", "n", "--cadence-seconds", "600", "--reasons", "every-tick,"},
		{"create", "--name", "n", "--cadence-seconds", "600", "--reasons", "unknown"},
		{"update", "cticker000000000000000000", "--quiet-hours", "{}", "--clear-quiet-hours"},
		{"update", "cticker000000000000000000", "--channel-ids", "c1", "--clear-channels"},
		{"get", "a-bare-name"},
		{"delete", "cticker000000000000000000"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls := 0
			gql, _ := captureGraphQLFunc(t, func(op string) string {
				calls++
				return `{"data":{"createTicker":` + tickerFixture + `,"updateTicker":` + tickerFixture + `,"deleteTicker":true,"ticker":null}}`
			})
			f, _ := testFactory(t)
			r := NewRootCmd(f)
			all := append([]string{"ticker"}, args...)
			all = append(all, "--app", "acme.com:ops", "--server", gql.URL)
			r.SetArgs(all)
			err := r.Execute()
			if err == nil || exitCodeFor(err) != 2 || calls != 0 {
				t.Fatalf("expected preflight usage refusal, got %v; calls %d", err, calls)
			}
		})
	}
}

func TestTickerUpdateOmitAndFalse(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"UpdateTicker": `{"data":{"updateTicker":` + tickerFixture + `}}`})
	f, _ := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"ticker", "update", "cticker000000000000000000", "--enabled=false", "--clear-channels", "--clear-quiet-hours", "--server", gql.URL})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	var v struct{ Input map[string]any }
	_ = json.Unmarshal(captured["UpdateTicker"], &v)
	if len(v.Input) != 3 || v.Input["enabled"] != false || v.Input["clearChannels"] != true || v.Input["clearQuietHours"] != true {
		t.Fatalf("wire %s", captured["UpdateTicker"])
	}
}

func TestTickerGetNull(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{"GetTicker": `{"data":{"ticker":null}}`})
	f, _ := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"ticker", "get", "cticker000000000000000000", "--server", gql.URL})
	if err := r.Execute(); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("%v", err)
	}
}

const tickerRef = "cticker000000000000000000"
const workerRef = "cworker000000000000000000"
const subscriptionFixture = `{"id":"s1","worker":{"id":"w1","name":"Worker","urn":null},"createdBy":null,"viaGrant":false,"createdAt":"2026-10-19T00:00:00Z","endedAt":null,"endedReason":null}`

func TestTickerSubscriptionOutcomes(t *testing.T) {
	for _, verb := range []string{"subscribe", "unsubscribe"} {
		for _, a := range []string{"null", `"POSTED"`, `"FAILED"`} {
			t.Run(verb+a, func(t *testing.T) {
				op, field := "SubscribeTicker", "subscribeWorker"
				if verb == "unsubscribe" {
					op, field = "UnsubscribeTicker", "unsubscribeWorker"
				}
				calls := 0
				gql, _ := captureGraphQLFunc(t, func(got string) string {
					if got == op {
						calls++
						return `{"data":{"` + field + `":{"subscription":` + subscriptionFixture + `,"announcement":` + a + `}}}`
					}
					return ""
				})
				f, out := testFactory(t)
				stderr := &strings.Builder{}
				f.IOStreams.ErrOut = stderr
				r := NewRootCmd(f)
				r.SetArgs([]string{"ticker", verb, tickerRef, "--worker", workerRef, "--json", "--server", gql.URL})
				if e := r.Execute(); e != nil {
					t.Fatal(e)
				}
				if calls != 1 {
					t.Fatalf("%d calls", calls)
				}
				var d map[string]json.RawMessage
				if e := json.Unmarshal([]byte(out.String()), &d); e != nil {
					t.Fatal(e)
				}
				if string(d["announcement"]) != a {
					t.Fatal(out.String())
				}
				if a == `"FAILED"` {
					if !strings.Contains(stderr.String(), "change stands") || !strings.Contains(stderr.String(), "do not rerun") {
						t.Fatal(stderr.String())
					}
				} else if stderr.Len() != 0 {
					t.Fatal(stderr.String())
				}
			})
		}
	}
}

func TestTickerGetListPageSubscribers(t *testing.T) {
	for _, list := range []bool{false, true} {
		t.Run(map[bool]string{false: "get", true: "list"}[list], func(t *testing.T) {
			page := 0
			gql, _ := captureGraphQLFunc(t, func(op string) string {
				switch op {
				case "GetTicker":
					return `{"data":{"ticker":` + tickerFixture + `}}`
				case "ListTickers":
					return `{"data":{"tickers":[` + tickerFixture + `]}}`
				case "TickerSubscribers":
					page++
					if page == 1 {
						return `{"data":{"ticker":{"subscribers":{"items":[],"hasNextPage":true,"endCursor":"next"}}}}`
					}
					return `{"data":{"ticker":{"subscribers":{"items":[` + subscriptionFixture + `],"hasNextPage":false,"endCursor":null}}}}`
				}
				return ""
			})
			f, out := testFactory(t)
			r := NewRootCmd(f)
			args := []string{"ticker", "get", tickerRef}
			if list {
				args = []string{"ticker", "list", "--app", "acme.com:ops"}
			}
			r.SetArgs(append(args, "--json", "--server", gql.URL))
			if e := r.Execute(); e != nil {
				t.Fatal(e)
			}
			if page != 2 || !strings.Contains(out.String(), `"id": "s1"`) || !strings.Contains(out.String(), `"readableChannels": []`) {
				t.Fatal(out.String(), page)
			}
		})
	}
}

func TestTickerRefusalsDoNotRetry(t *testing.T) {
	for _, code := range []string{"TICKER_CADENCE_NEEDS_CONSENT", "FORBIDDEN", "UNAUTHENTICATED", "APP_MANAGER_REQUIRED", "TICKER_CADENCE_OUT_OF_RANGE", "SUBSCRIBE_CADENCE_NEEDS_HOLDER"} {
		t.Run(code, func(t *testing.T) {
			calls := 0
			gql, _ := captureGraphQLFunc(t, func(op string) string {
				if op == "UpdateTicker" {
					calls++
					return `{"errors":[{"message":"` + code + `: whole edit refused","extensions":{"code":"` + code + `"}}]}`
				}
				return ""
			})
			f, out := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs([]string{"ticker", "update", tickerRef, "--reasons", "every-tick", "--server", gql.URL})
			e := r.Execute()
			if e == nil || !strings.Contains(e.Error(), code) || calls != 1 || out.Len() != 0 {
				t.Fatalf("error=%v calls=%d output=%s", e, calls, out.String())
			}
		})
	}
}

func TestTickerDeleteConfirmation(t *testing.T) {
	for _, answer := range []string{"y\n", "n\n"} {
		t.Run(answer, func(t *testing.T) {
			calls := 0
			gql, _ := captureGraphQLFunc(t, func(op string) string {
				if op == "DeleteTicker" {
					calls++
					return `{"data":{"deleteTicker":true}}`
				}
				return ""
			})
			f, _, stderr := testFactoryTTY(t, answer)
			r := NewRootCmd(f)
			r.SetArgs([]string{"ticker", "delete", tickerRef, "--server", gql.URL})
			e := r.Execute()
			if answer == "y\n" {
				if e != nil || calls != 1 {
					t.Fatalf("%v %d", e, calls)
				}
			} else if e == nil || calls != 0 {
				t.Fatalf("%v %d", e, calls)
			}
			if !strings.Contains(stderr.String(), "cannot be undone") {
				t.Fatal(stderr.String())
			}
		})
	}
}

func TestTickerSubscribersRefusesStuckCursor(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{"GetTicker": `{"data":{"ticker":` + tickerFixture + `}}`, "TickerSubscribers": `{"data":{"ticker":{"subscribers":{"items":[],"hasNextPage":true,"endCursor":"same"}}}}`})
	f, out := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"ticker", "get", tickerRef, "--server", gql.URL})
	if e := r.Execute(); e == nil || !strings.Contains(e.Error(), "non-advancing") {
		t.Fatal(e)
	}
	if out.Len() != 0 {
		t.Fatal(out.String())
	}
}

func TestTickerEmptyListScopeAndDecorationFailure(t *testing.T) {
	for _, identity := range []string{`{"data":{"app":null}}`, `{"errors":[{"message":"unreadable","extensions":{"code":"FORBIDDEN"}}]}`} {
		t.Run(identity, func(t *testing.T) {
			gql := fakeGraphQL(t, map[string]string{"ListTickers": `{"data":{"tickers":[]}}`, "TeamAppIdentity": identity})
			f, out := testFactory(t)
			r := NewRootCmd(f)
			r.SetArgs([]string{"ticker", "list", "--app", "acme.com:ops", "--server", gql.URL})
			if e := r.Execute(); e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(out.String(), "app: hrn:app:acme.com:ops (from --app)") {
				t.Fatal(out.String())
			}
		})
	}
}

func TestTickerExplicitInputsAndNarrowing(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"UpdateTicker": `{"data":{"updateTicker":` + tickerFixture + `}}`})
	f, _ := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"ticker", "update", tickerRef, "--name", "renamed", "--cadence-seconds", "900", "--anchor-at", "2026-10-19T12:00:00Z", "--quiet-hours", `{"timezone":"UTC","windows":[]}`, "--channel-ids", "c1,c2", "--relevance", "mentions", "--reasons", "read-team-channel", "--enabled=false", "--json", "--server", gql.URL})
	if e := r.Execute(); e != nil {
		t.Fatal(e)
	}
	var v struct{ Input map[string]any }
	if e := json.Unmarshal(captured["UpdateTicker"], &v); e != nil {
		t.Fatal(e)
	}
	if len(v.Input) != 8 || v.Input["name"] != "renamed" || v.Input["cadenceSeconds"] != float64(900) || v.Input["anchorAt"] != "2026-10-19T12:00:00Z" || v.Input["relevance"] != "MENTIONS" || v.Input["enabled"] != false {
		t.Fatalf("wire %s", captured["UpdateTicker"])
	}
	for key, want := range map[string]string{"channelIds": `["c1","c2"]`, "reasons": `["READ_TEAM_CHANNEL"]`, "quietHours": `{"timezone":"UTC","windows":[]}`} {
		got, e := json.Marshal(v.Input[key])
		if e != nil || string(got) != want {
			t.Fatalf("%s: %s, %v", key, got, e)
		}
	}
}

func TestTickerWriteFailureDoesNotRepeatMutation(t *testing.T) {
	calls := 0
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		if op == "UpdateTicker" {
			calls++
			return `{"data":{"updateTicker":` + tickerFixture + `}}`
		}
		return ""
	})
	f, _ := testFactory(t)
	f.IOStreams.Out = output.Tracked(failingWriter{})
	r := NewRootCmd(f)
	r.SetArgs([]string{"ticker", "update", tickerRef, "--name", "renamed", "--json", "--server", gql.URL})
	if err := r.Execute(); err == nil || calls != 1 {
		t.Fatalf("%v, calls %d", err, calls)
	}
}

func TestTickerEmptyCollectionsAndConfiguredScope(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{"ListTickers": `{"data":{"tickers":[]}}`, "GetTicker": `{"data":{"ticker":` + tickerFixture + `}}`, "TickerSubscribers": `{"data":{"ticker":{"subscribers":{"items":[],"hasNextPage":false,"endCursor":null}}}}`, "TeamAppIdentity": `{"data":{"app":{"id":"app1","urn":"hrn:app:acme.com:ops","name":"Ops"}}}`})
	for _, args := range [][]string{{"list"}, {"get", tickerRef, "--json"}, {"list", "--json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f, out := testFactory(t)
			cfg, e := f.Config()
			if e != nil {
				t.Fatal(e)
			}
			if e = cfg.Set("app", "acme.com:ops"); e != nil {
				t.Fatal(e)
			}
			r := NewRootCmd(f)
			all := append([]string{"ticker"}, args...)
			r.SetArgs(append(all, "--server", gql.URL))
			if e = r.Execute(); e != nil {
				t.Fatal(e)
			}
			if len(args) == 1 {
				if !strings.Contains(out.String(), "(from the App context)") || !strings.Contains(out.String(), "hrn:app:acme.com:ops") {
					t.Fatal(out.String())
				}
			} else if args[0] == "get" {
				for _, field := range []string{"subscribers", "readableChannels"} {
					if !strings.Contains(out.String(), `"`+field+`": []`) {
						t.Fatal(out.String())
					}
				}
			} else if strings.TrimSpace(out.String()) != "[]" {
				t.Fatal(out.String())
			}
		})
	}
}

func TestTickerUncertainMutationNoRetry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("upstream unavailable"))
	}))
	defer srv.Close()
	f, _ := testFactory(t)
	r := NewRootCmd(f)
	r.SetArgs([]string{"ticker", "update", tickerRef, "--name", "renamed", "--server", srv.URL})
	err := r.Execute()
	if err == nil || calls != 1 || exitCodeFor(err) != 7 || !strings.Contains(err.Error(), "verify the current state before retrying") {
		t.Fatalf("error %v, calls %d", err, calls)
	}
}
