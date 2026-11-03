package cmd

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1291 (CHECKED T1, companion of hadron-server#2096): `ticker
// apply-templates --worker <ref>` runs applyAgentTickerTemplates for one
// worker, as the person who manages the App (a worker session is refused by
// the server, so the call never carries one). The receipt lists what was
// created, with its effective cadence, and the templates already present.

const c1291Worker = "hrn:worker:acme.com:ops:iris"

func c1291Ticker(id, name string, cadence int) string {
	return `{"id":"` + id + `","urn":"hrn:ticker:acme.com:ops:` + name + `","name":"` + name + `","slug":"` + name + `","app":{"id":"app1"},"cadenceSeconds":` + strconv.Itoa(cadence) +
		`,"anchorAt":"2026-10-30T00:00:00Z","quietHours":null,"channels":[],"relevance":"ALL","reasons":["EVERY_TICK"],"enabled":true,"owner":{"id":"wkr1","name":"Iris","urn":null},` +
		`"nextTickAt":null,"lastTickAt":null,"createdAt":"2026-10-30T00:00:00Z","updatedAt":null,"tickCode":"","registerTaskLoc":null}`
}

func c1291Created(id, name, template string, cadence int) string {
	return `{"ticker":` + c1291Ticker(id, name, cadence) + `,"template":{"id":"tpl-` + name + `","name":"` + template + `"},"effectiveCadenceSeconds":` + strconv.Itoa(cadence) + `}`
}

func c1291Result(created, present string) string {
	return `{"data":{"applyAgentTickerTemplates":{"created":[` + created + `],"alreadyPresent":[` + present + `]}}}`
}

func c1291Responses(result string, floor int) map[string]string {
	return map[string]string{
		"ApplyAgentTickerTemplates": result,
		"TickerAppFloor":            `{"data":{"appTickerSettings":{"minCadenceSeconds":` + strconv.Itoa(floor) + `}}}`,
	}
}

func TestC1291AppliesForOneWorkerAsThePerson(t *testing.T) {
	result := c1291Result(c1291Created("tk1", "standup", "Standup", 1800)+","+c1291Created("tk2", "review", "Review", 3600), `{"id":"tpl-daily","name":"Daily check"}`)
	calls, out, err := runTeamTask(t, c1291Responses(result, 1800), "ticker", "apply-templates", "--worker", c1291Worker)
	if err != nil {
		t.Fatal(err)
	}
	applies := stopCalls(calls, "ApplyAgentTickerTemplates")
	if len(applies) != 1 {
		t.Fatalf("apply calls %d", len(applies))
	}
	if applies[0].Vars["workerRef"] != c1291Worker {
		t.Fatalf("workerRef %v", applies[0].Vars)
	}
	// The server refuses a worker session: a bound worktree must not send one.
	if applies[0].Session != "" {
		t.Fatalf("apply carried worker session %q", applies[0].Session)
	}
	floors := stopCalls(calls, "TickerAppFloor")
	if len(floors) != 1 || floors[0].Vars["appRef"] != "app1" {
		t.Fatalf("floor read %+v", floors)
	}
	for _, want := range []string{"created 2", "standup", "Standup", "every 30m (the App's floor)", "review", "Review", "every 1h", "already present 1", "Daily check"} {
		if !strings.Contains(out, want) {
			t.Fatalf("receipt lacks %q:\n%s", want, out)
		}
	}
	// Bounded cadence copy: above the floor gets no label, and nothing ever
	// claims the floor raised a cadence (the client cannot know).
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "review") && strings.Contains(line, "floor") {
			t.Fatalf("above-floor ticker labelled: %q", line)
		}
	}
	if strings.Contains(strings.ToLower(out), "raised") {
		t.Fatalf("receipt claims a raise:\n%s", out)
	}
}

func TestC1291ReceiptJSON(t *testing.T) {
	result := c1291Result(c1291Created("tk1", "standup", "Standup", 1800), `{"id":"tpl-daily","name":"Daily check"}`)
	_, out, err := runTeamTask(t, c1291Responses(result, 600), "ticker", "apply-templates", "--worker", c1291Worker, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Created []struct {
			Ticker struct {
				ID             string `json:"id"`
				Name           string `json:"name"`
				Slug           string `json:"slug"`
				CadenceSeconds int    `json:"cadenceSeconds"`
			} `json:"ticker"`
			Template struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"template"`
			EffectiveCadenceSeconds int `json:"effectiveCadenceSeconds"`
		} `json:"created"`
		AlreadyPresent []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"alreadyPresent"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(dto.Created) != 1 || dto.Created[0].Ticker.ID != "tk1" || dto.Created[0].Ticker.Slug != "standup" || dto.Created[0].Template.ID != "tpl-standup" ||
		dto.Created[0].Template.Name != "Standup" || dto.Created[0].EffectiveCadenceSeconds != 1800 {
		t.Fatalf("created %s", out)
	}
	if len(dto.AlreadyPresent) != 1 || dto.AlreadyPresent[0].ID != "tpl-daily" || dto.AlreadyPresent[0].Name != "Daily check" {
		t.Fatalf("alreadyPresent %s", out)
	}
	// --json is the server's result: no client-derived floor label in it.
	if strings.Contains(out, "floor") {
		t.Fatalf("json carries a client label: %s", out)
	}
}

// A second run is a visible no-op: exit 0, "nothing to create", and the
// templates already present. Nothing was created, so no floor read is made.
func TestC1291SecondRunIsANoOp(t *testing.T) {
	result := c1291Result("", `{"id":"tpl-standup","name":"Standup"},{"id":"tpl-review","name":"Review"}`)
	calls, out, err := runTeamTask(t, c1291Responses(result, 600), "ticker", "apply-templates", "--worker", c1291Worker)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"nothing to create", "already present 2", "Standup", "Review"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no-op receipt lacks %q:\n%s", want, out)
		}
	}
	if len(stopCalls(calls, "TickerAppFloor")) != 0 {
		t.Fatal("floor read made with nothing created")
	}
	_, out, err = runTeamTask(t, c1291Responses(result, 600), "ticker", "apply-templates", "--worker", c1291Worker, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"created": []`) {
		t.Fatalf("empty created must render []: %s", out)
	}
}

// An unreadable floor drops the label; it never fails the receipt of a write
// that already happened.
func TestC1291UnreadableFloorOmitsLabel(t *testing.T) {
	r := c1291Responses(c1291Result(c1291Created("tk1", "standup", "Standup", 1800), ""), 0)
	r["TickerAppFloor"] = c1291Errors("FORBIDDEN")
	_, out, err := runTeamTask(t, r, "ticker", "apply-templates", "--worker", c1291Worker)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "every 30m") || strings.Contains(out, "floor") {
		t.Fatalf("receipt %q", out)
	}
}

func TestC1291RefusalsKeepTheirCode(t *testing.T) {
	for _, tc := range []struct {
		code     string
		exit     int
		noneMade bool
	}{
		{"FORBIDDEN", exitcode.Forbidden, false},
		{"WORKER_NOT_FOUND", exitcode.NotFound, false},
		{"WORKER_AGENT_NOT_INSTALLED", exitcode.Usage, false},
		{"TICKER_LIMIT_REACHED", exitcode.Conflict, true},
		{"TICKER_NAME_TAKEN", exitcode.Conflict, true},
	} {
		t.Run(tc.code, func(t *testing.T) {
			_, _, err := runTeamTask(t, c1291Responses(c1291Errors(tc.code), 600), "ticker", "apply-templates", "--worker", c1291Worker)
			if err == nil || !strings.HasPrefix(err.Error(), tc.code+": ") {
				t.Fatalf("err %v, want %s prefix", err, tc.code)
			}
			if got := exitCodeFor(err); got != tc.exit {
				t.Fatalf("exit %d, want %d", got, tc.exit)
			}
			if made := strings.Contains(err.Error(), "nothing was created"); made != tc.noneMade {
				t.Fatalf("nothing-created wording %v, want %v: %v", made, tc.noneMade, err)
			}
		})
	}
}

func TestC1291WorkerIsRequiredBeforeAnyRequest(t *testing.T) {
	for _, argv := range [][]string{
		{"ticker", "apply-templates"},
		{"ticker", "apply-templates", "--worker", ""},
		{"ticker", "apply-templates", "--worker", "Iris"},
	} {
		calls, _, err := runTeamTask(t, c1291Responses(c1291Result("", ""), 600), argv...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("%v: err %v, want exit %d", argv, err, exitcode.Usage)
		}
		if len(stopCalls(calls, "ApplyAgentTickerTemplates")) != 0 {
			t.Fatalf("%v: reached the server", argv)
		}
	}
}

func TestC1291HelpSaysWhoMayRunIt(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"ticker", "apply-templates", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"manages the App", "worker session", "one worker", "alreadyPresent", "nothing was created"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(strings.ToLower(out.String()), "raised") {
		t.Fatalf("help claims a raise:\n%s", out.String())
	}
}

func c1291Errors(code string) string {
	return `{"data":null,"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}]}`
}

// cli#1296: the server's all-or-nothing messages already end "nothing was
// created." (resolvers.ticker.ts), so the CLI adds the clause only when the
// message lacks it: exactly once either way.
func TestC1296NothingCreatedSaidOnce(t *testing.T) {
	for _, code := range []string{"TICKER_LIMIT_REACHED", "TICKER_NAME_TAKEN"} {
		for _, msg := range []string{"refused; nothing was created.", "Refused; Nothing Was Created", "refused"} {
			t.Run(code+"/"+msg, func(t *testing.T) {
				body := `{"data":null,"errors":[{"message":"` + msg + `","extensions":{"code":"` + code + `"}}]}`
				_, _, err := runTeamTask(t, c1291Responses(body, 600), "ticker", "apply-templates", "--worker", c1291Worker)
				if err == nil || !strings.HasPrefix(err.Error(), code+": ") {
					t.Fatalf("err %v, want %s prefix", err, code)
				}
				if exitCodeFor(err) != exitcode.Conflict {
					t.Fatalf("exit %d, want %d", exitCodeFor(err), exitcode.Conflict)
				}
				if n := strings.Count(strings.ToLower(err.Error()), "nothing was created"); n != 1 {
					t.Fatalf("clause %d times: %v", n, err)
				}
			})
		}
	}
}
