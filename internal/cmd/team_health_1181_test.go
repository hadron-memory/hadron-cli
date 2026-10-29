package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1181: `team health check` — key workers' last activity against their
// tickers, read off-host with --app and judged client-side. The exit code is
// the alarm: 0 healthy, 9 DARK or nobody to watch, 3/7/8 per the contract.
// These run unbound, the way the scheduled job runs.

const health1181App = "hrn:app:acme.com:eng-team"

// health1181World is one server state. Times are on 2026-10-26 (a Monday),
// asOf 12:00:00Z; the every-5-minutes ticker anchored at 00:00Z has a tick at
// 11:55 (within the 5m slack, not due) and 11:50 (due).
type health1181World struct {
	teamPaused bool
	roster     string // the workers array
	tickers    string // the tickers array
	// subscribers per ticker id: each element is one page's items.
	pages map[string][]string
	// errors per operation name: the whole response body.
	fail map[string]string
}

func health1181Party(id, name string) string {
	return fmt.Sprintf(`{"id":%q,"kind":"WORKER","name":%q,"urn":"hrn:worker:acme.com:eng-team:%s"}`, id, name, strings.ToLower(name))
}

func health1181Entry(id, name, role string, lastActive, pausedUntil, status, reason string) string {
	q := func(s string) string {
		if s == "" {
			return "null"
		}
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf(`{"worker":%s,"role":%q,"liveSession":true,"lastActiveAt":%s,"pausedUntil":%s,"checkStatus":{"status":%q,"pendingReason":%s,"lastCheckedAt":null}}`,
		health1181Party(id, name), role, q(lastActive), q(pausedUntil), status, q(reason))
}

func health1181Ticker(id, slug string, cadence int, enabled bool, quiet string) string {
	return health1181TickerReasons(id, slug, cadence, enabled, quiet, `"EVERY_TICK"`)
}

func health1181TickerReasons(id, slug string, cadence int, enabled bool, quiet, reasons string) string {
	if quiet == "" {
		quiet = "null"
	}
	return fmt.Sprintf(`{"id":%q,"urn":null,"name":%q,"slug":%q,"app":{"id":"capp1"},"cadenceSeconds":%d,"anchorAt":"2026-10-26T00:00:00Z","quietHours":%s,
	 "channels":[],"relevance":"ALL","reasons":[%s],"enabled":%t,"owner":null,"nextTickAt":null,"lastTickAt":null,"createdAt":"2026-10-01T00:00:00Z","updatedAt":null}`,
		id, slug, slug, cadence, quiet, reasons, enabled)
}

func health1181Sub(workerID string) string {
	return fmt.Sprintf(`{"id":"sub-%s","worker":{"id":%q,"name":"w","urn":null},"createdBy":null,"viaGrant":false,"createdAt":"2026-10-01T00:00:00Z","endedAt":null,"endedReason":null}`, workerID, workerID)
}

func (w health1181World) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			rw.Header().Set("Content-Type", "application/json")
			_, _ = rw.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		rw.Header().Set("Content-Type", "application/json")
		if resp, ok := w.fail[body.OperationName]; ok {
			_, _ = rw.Write([]byte(resp))
			return
		}
		var resp string
		switch body.OperationName {
		case "TeamHealthRoster":
			resp = `{"data":{"teamRoster":{"appId":"capp1","asOf":"2026-10-26T12:00:00Z","workers":[` + w.roster + `]}}}`
		case "TeamHealthApp":
			paused := "null"
			if w.teamPaused {
				paused = `"2026-10-26T09:00:00Z"`
			}
			resp = `{"data":{"app":{"id":"capp1","teamPausedAt":` + paused + `}}}`
		case "ListTickers":
			resp = `{"data":{"tickers":[` + w.tickers + `]}}`
		case "TickerSubscribers":
			ref, _ := body.Variables["ref"].(string)
			page := 0
			if after, ok := body.Variables["after"].(string); ok {
				_, _ = fmt.Sscanf(after, "c%d", &page)
			}
			pages := w.pages[ref]
			items := ""
			if page < len(pages) {
				items = pages[page]
			}
			more, cursor := page+1 < len(pages), fmt.Sprintf(`"c%d"`, page+1)
			resp = fmt.Sprintf(`{"data":{"ticker":{"subscribers":{"items":[%s],"hasNextPage":%t,"endCursor":%s}}}}`, items, more, cursor)
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		_, _ = rw.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runHealth1181(t *testing.T, w health1181World, extra ...string) (string, int) {
	t.Helper()
	srv := w.serve(t)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL, "--app", health1181App, "team", "health", "check"}, extra...))
	err := root.Execute()
	return out.String(), exitCodeFor(err)
}

// health1181Healthy: Ada reacted after the due 11:50 tick, Petra too (on page
// two of the subscribers), and Bob is an engineer, not a key worker.
func health1181Healthy() health1181World {
	return health1181World{
		roster: strings.Join([]string{
			health1181Entry("w-ada", "Ada", "coordinator", "2026-10-26T11:51:00Z", "", "OK", ""),
			health1181Entry("w-petra", "Petra", "project-manager", "2026-10-26T11:52:00Z", "", "OK", ""),
			health1181Entry("w-bob", "Bob", "server-engineer", "2026-10-20T00:00:00Z", "", "OK", ""),
		}, ","),
		tickers: health1181Ticker("t1", "coordinator-tick", 300, true, ""),
		pages:   map[string][]string{"t1": {health1181Sub("w-ada") + "," + health1181Sub("w-bob"), health1181Sub("w-petra")}},
	}
}

func TestHealthCheckOKExitsZero(t *testing.T) {
	out, code := runHealth1181(t, health1181Healthy())
	if code != exitcode.OK {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	for _, want := range []string{"Ada", "coordinator", "Petra", "project-manager", "OK", "healthy as of 2026-10-26T12:00:00Z (slack 5m0s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("text missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Bob") {
		t.Errorf("a worker outside the key roles is not listed:\n%s", out)
	}
}

func TestHealthCheckJSONShape(t *testing.T) {
	w := health1181Healthy()
	w.roster += "," + health1181Entry("w-cy", "Cy", "coordinator", "2026-10-26T11:00:00Z", "", "OK", "")
	w.pages["t1"][1] += "," + health1181Sub("w-cy")
	out, code := runHealth1181(t, w, "--json")
	if code != exitcode.HealthDark {
		t.Fatalf("exit %d, want 9:\n%s", code, out)
	}
	var got struct {
		AppID        string   `json:"appId"`
		AsOf         string   `json:"asOf"`
		SlackSeconds int      `json:"slackSeconds"`
		Roles        []string `json:"roles"`
		Healthy      bool     `json:"healthy"`
		Workers      []struct {
			Worker       map[string]any `json:"worker"`
			Role         string         `json:"role"`
			Verdict      string         `json:"verdict"`
			Reason       string         `json:"reason"`
			LastActiveAt *string        `json:"lastActiveAt"`
			DueTickAt    *string        `json:"dueTickAt"`
			DueTicker    *string        `json:"dueTicker"`
			Tickers      []string       `json:"tickers"`
		} `json:"workers"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.AppID != "capp1" || got.AsOf != "2026-10-26T12:00:00Z" || got.SlackSeconds != 300 || got.Healthy {
		t.Errorf("envelope: %+v", got)
	}
	if strings.Join(got.Roles, ",") != "coordinator,program-manager,project-manager" {
		t.Errorf("default roles: %v", got.Roles)
	}
	if len(got.Workers) != 3 {
		t.Fatalf("workers: %+v", got.Workers)
	}
	cy := got.Workers[2]
	if cy.Worker["urn"] != "hrn:worker:acme.com:eng-team:cy" || cy.Verdict != "DARK" || cy.DueTickAt == nil || *cy.DueTickAt != "2026-10-26T11:50:00Z" ||
		cy.DueTicker == nil || *cy.DueTicker != "coordinator-tick" || len(cy.Tickers) != 1 {
		t.Errorf("DARK row: %+v", cy)
	}
}

func TestHealthCheckPagedSubscribersAreAllRead(t *testing.T) {
	// Petra subscribes only on the SECOND page: a single-page read would have
	// no counted ticker for her and SKIP her instead of judging her.
	w := health1181Healthy()
	w.roster = health1181Entry("w-petra", "Petra", "project-manager", "2026-10-26T11:00:00Z", "", "OK", "")
	out, code := runHealth1181(t, w)
	if code != exitcode.HealthDark || !strings.Contains(out, "DARK") || strings.Contains(out, "no counted ticker") {
		t.Fatalf("the second subscriber page must be read (exit %d):\n%s", code, out)
	}
}

func TestHealthCheckDarkStates(t *testing.T) {
	cases := map[string]struct {
		entry  string
		reason string
	}{
		"missed the due tick": {health1181Entry("w-ada", "Ada", "coordinator", "2026-10-26T11:49:59Z", "", "OK", ""), "no activity since the tick at 2026-10-26T11:50:00Z"},
		"never driven":        {health1181Entry("w-ada", "Ada", "coordinator", "", "", "NO_SESSION", ""), "never driven"},
		// NO_TASK is a PENDING reason, but a key worker with no task is still ticked.
		"no task is not a skip": {health1181Entry("w-ada", "Ada", "coordinator", "2026-10-26T11:00:00Z", "", "PENDING", "NO_TASK"), "no activity since"},
		// an expired own pause is not a pause
		"expired pause": {health1181Entry("w-ada", "Ada", "coordinator", "2026-10-26T11:00:00Z", "2026-10-26T11:30:00Z", "OK", ""), "no activity since"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := health1181Healthy()
			w.roster = c.entry
			out, code := runHealth1181(t, w)
			if code != exitcode.HealthDark || !strings.Contains(out, "DARK") || !strings.Contains(out, c.reason) {
				t.Fatalf("exit %d, want 9 with %q:\n%s", code, c.reason, out)
			}
		})
	}
}

func TestHealthCheckSkippedStates(t *testing.T) {
	stale := "2026-10-26T08:00:00Z"
	cases := map[string]struct {
		world  func(*health1181World)
		reason string
	}{
		"team paused": {func(w *health1181World) { w.teamPaused = true }, "team paused"},
		"own pause, no session": {func(w *health1181World) {
			w.roster = health1181Entry("w-ada", "Ada", "coordinator", stale, "2026-10-26T13:00:00Z", "NO_SESSION", "")
		}, "paused"},
		"stop signal": {func(w *health1181World) {
			w.roster = health1181Entry("w-ada", "Ada", "coordinator", stale, "", "PENDING", "STOPPED")
		}, "stop signal"},
		"isolated": {func(w *health1181World) {
			w.roster = health1181Entry("w-ada", "Ada", "coordinator", stale, "", "PENDING", "ISOLATED")
		}, "isolated"},
		"no ticker": {func(w *health1181World) {
			w.roster = health1181Entry("w-ada", "Ada", "coordinator", stale, "", "OK", "")
			w.pages = map[string][]string{"t1": {health1181Sub("w-bob")}}
		}, "no counted ticker"},
		// Marco's ruling (#44783): a ticker that fires only on unread proves nothing.
		"read-team-channel only does not count": {func(w *health1181World) {
			w.roster = health1181Entry("w-ada", "Ada", "coordinator", stale, "", "OK", "")
			w.tickers = health1181TickerReasons("t1", "eden-queue-tick", 300, true, "", `"READ_TEAM_CHANNEL"`)
		}, "no counted ticker"},
		"disabled ticker does not count": {func(w *health1181World) {
			w.roster = health1181Entry("w-ada", "Ada", "coordinator", stale, "", "OK", "")
			w.tickers = health1181Ticker("t1", "coordinator-tick", 300, false, "")
		}, "no counted ticker"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := health1181Healthy()
			w.roster = health1181Entry("w-ada", "Ada", "coordinator", stale, "", "OK", "")
			c.world(&w)
			out, code := runHealth1181(t, w)
			if code != exitcode.OK || !strings.Contains(out, "SKIPPED") || !strings.Contains(out, c.reason) {
				t.Fatalf("exit %d, want 0 with SKIPPED %q:\n%s", code, c.reason, out)
			}
		})
	}
}

func TestHealthCheckQuietHoursSilenceIsNotDark(t *testing.T) {
	// Quiet 10:00-13:00 UTC every day: the last non-quiet tick before the
	// cutoff is 09:55, and Ada was active at 09:56 — silent since, by design.
	quiet := `{"timezone":"UTC","windows":[{"days":[1,2,3,4,5,6,7],"from":"10:00","to":"13:00"}]}`
	w := health1181Healthy()
	w.roster = health1181Entry("w-ada", "Ada", "coordinator", "2026-10-26T09:56:00Z", "", "OK", "")
	w.tickers = health1181Ticker("t1", "coordinator-tick", 300, true, quiet)
	out, code := runHealth1181(t, w, "--json")
	if code != exitcode.OK || !strings.Contains(out, `"dueTickAt": "2026-10-26T09:55:00Z"`) {
		t.Fatalf("quiet hours: exit %d:\n%s", code, out)
	}
}

func TestHealthCheckEmptyKeyWorkerSetExitsNine(t *testing.T) {
	for _, args := range [][]string{nil, {"--role", "program-manager"}, {"--json"}} {
		w := health1181Healthy()
		w.roster = health1181Entry("w-bob", "Bob", "server-engineer", "2026-10-26T11:59:00Z", "", "OK", "")
		out, code := runHealth1181(t, w, args...)
		if code != exitcode.HealthDark {
			t.Fatalf("%v: nobody to watch must exit 9, got %d:\n%s", args, code, out)
		}
		if len(args) == 1 && args[0] == "--json" {
			if !strings.Contains(out, `"healthy": false`) || !strings.Contains(out, `"workers": []`) {
				t.Errorf("empty set JSON:\n%s", out)
			}
		} else if !strings.Contains(out, "no key worker found") {
			t.Errorf("empty set text:\n%s", out)
		}
	}
}

func TestHealthCheckRoleFlagNarrows(t *testing.T) {
	w := health1181Healthy()
	w.roster += "," + health1181Entry("w-cy", "Cy", "project-manager", "2026-10-26T11:00:00Z", "", "OK", "")
	w.pages["t1"][1] += "," + health1181Sub("w-cy")
	out, code := runHealth1181(t, w, "--role", "Coordinator")
	if code != exitcode.OK || strings.Contains(out, "Cy") || strings.Contains(out, "Petra") || !strings.Contains(out, "Ada") {
		t.Fatalf("--role coordinator (any case) watches coordinators only, exit %d:\n%s", code, out)
	}
}

func TestHealthCheckFailuresNeverExitZero(t *testing.T) {
	gqlErr := func(code string) string {
		return `{"errors":[{"message":"no","extensions":{"code":"` + code + `"}}],"data":null}`
	}
	cases := map[string]struct {
		op, body string
		want     int
	}{
		"roster refused":      {"TeamHealthRoster", gqlErr("FORBIDDEN"), exitcode.Forbidden},
		"credentials refused": {"TeamHealthRoster", gqlErr("UNAUTHENTICATED"), exitcode.AuthRequired},
		"null roster":         {"TeamHealthRoster", `{"data":{"teamRoster":null}}`, exitcode.Unavailable},
		"tickers refused":     {"ListTickers", gqlErr("FORBIDDEN"), exitcode.Forbidden},
		"non-advancing page": {"TickerSubscribers",
			`{"data":{"ticker":{"subscribers":{"items":[],"hasNextPage":true,"endCursor":null}}}}`, exitcode.Unavailable},
		"repeated cursor": {"TickerSubscribers",
			`{"data":{"ticker":{"subscribers":{"items":[],"hasNextPage":true,"endCursor":"same"}}}}`, exitcode.Unavailable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := health1181Healthy()
			w.fail = map[string]string{c.op: c.body}
			out, code := runHealth1181(t, w)
			if code != c.want {
				t.Fatalf("exit %d, want %d:\n%s", code, c.want, out)
			}
			if out != "" {
				t.Errorf("a failed check renders nothing on stdout:\n%s", out)
			}
		})
	}
}

func TestHealthCheckUnreachableServerExitsSeven(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "--app", health1181App, "team", "health", "check"})
	if code := exitCodeFor(root.Execute()); code != exitcode.Unavailable {
		t.Fatalf("a gateway 502 exits %d, want 7", code)
	}
}

func TestHealthCheckIsReadOnly(t *testing.T) {
	// The fake answers only the four reads; any other operation (a write)
	// fails the test via t.Errorf in serve.
	if _, code := runHealth1181(t, health1181Healthy()); code != exitcode.OK {
		t.Fatalf("exit %d", code)
	}
}

func TestHealthCheckLiveCheckTickerHasOneCadenceOfGrace(t *testing.T) {
	// A 5-minute live-check ticker: the due tick is the latest strictly
	// before 12:00 - (5m slack + 5m grace) = 11:50, so 11:45 (11:50 itself
	// is not due yet). Activity at 11:46 is OK; at 11:44 it is DARK.
	for last, want := range map[string]int{"2026-10-26T11:46:00Z": exitcode.OK, "2026-10-26T11:44:00Z": exitcode.HealthDark} {
		w := health1181Healthy()
		w.roster = health1181Entry("w-ada", "Ada", "coordinator", last, "", "OK", "")
		w.tickers = health1181TickerReasons("t1", "team-tick", 300, true, "", `"READ_TEAM_CHANNEL","LIVE_CHECK"`)
		out, code := runHealth1181(t, w, "--json")
		if code != want || !strings.Contains(out, `"dueTickAt": "2026-10-26T11:45:00Z"`) {
			t.Fatalf("last active %s: exit %d, want %d:\n%s", last, code, want, out)
		}
	}
}
