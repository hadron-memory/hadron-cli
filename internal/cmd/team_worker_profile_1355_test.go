package cmd

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// cli#1355 / hadron-server#1149 — `worker get` shows appName/ownerName and
// renders a PROFILE-tier read (a signed-in non-participant naming a worker by
// id or URN) without claiming anything about the masked working state.

const (
	profileNames   = `{"data":{"worker":{"appName":"Eng Team","ownerName":"acme.com","appUrn":"hrn:app:acme.com:eng-team"}}}`
	profileNamesNA = `{"data":{"worker":{"appName":null,"ownerName":null,"appUrn":null}}}`
	// What a server that predates #1149 answers to the names selection.
	oldServerNames = `{"errors":[{"message":"Cannot query field \"appName\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	profileHexID   = "0123456789abcdef0123456789abcdef"
)

// profileWorkerJSON is a worker as a non-participant reads it: identity scalars
// only; every working-state field null (repos [] and reportsToRetired false are
// the masked spellings of the two non-null fields).
const profileWorkerJSON = `{"id":"` + profileHexID + `","urn":"hrn:worker:acme.com:eng-team:iris","slug":"iris",
	"appId":"capp100000000000000000000","agentId":"agt1","name":"Iris","role":"backend-engineer",
	"prompt":null,"signature":null,"trailer":null,"memoryId":null,"family":null,"hasLiveSession":null,
	"reportsTo":null,"reportsToRetired":false,"repos":[],"checkStatus":null,"bootPromptRef":null,
	"retiredAt":null,"retiredBy":null,"createdAt":"2026-08-14T00:00:00Z","createdBy":null}`

type profileOpLog struct {
	mu  sync.Mutex
	ops []string
}

func (l *profileOpLog) add(op string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ops = append(l.ops, op)
}

func (l *profileOpLog) count(op string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, o := range l.ops {
		if o == op {
			n++
		}
	}
	return n
}

func (l *profileOpLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.ops, ",")
}

// runWorkerGet runs `team worker get` against a fake that answers GetWorker and
// GetWorkerProfile with the given bodies and records every operation sent.
func runWorkerGet(t *testing.T, worker, names string, args ...string) (string, *profileOpLog, error) {
	t.Helper()
	teamGitDir(t)
	log := &profileOpLog{}
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		log.add(op)
		switch op {
		case "GetWorker":
			return `{"data":{"worker":` + worker + `}}`
		case "GetWorkerProfile":
			return names
		case "TeamAppIdentity":
			return teamAppIdentityJSON
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "worker", "get"}, append(args, "--server", gql.URL)...))
	err := root.Execute()
	return out.String(), log, err
}

func TestWorkerGetProfileByURNIsDirectAndHonest(t *testing.T) {
	for _, ref := range []string{"hrn:worker:acme.com:eng-team:iris", profileHexID} {
		t.Run(ref, func(t *testing.T) {
			// --app is given on purpose: an ambient scope must not trigger a
			// roster scan or an App lookup for a self-contained reference.
			out, log, err := runWorkerGet(t, profileWorkerJSON, profileNames, ref, "--app", "acme.com:eng-team")
			if err != nil {
				t.Fatalf("execute: %v\n%s", err, out)
			}
			if got := log.String(); got != "GetWorker,GetWorkerProfile,GetWorkerTerminal,GetWorkerModelGroups" {
				t.Errorf("operations = %s, want exactly GetWorker,GetWorkerProfile,GetWorkerTerminal,GetWorkerModelGroups (no roster, no App lookup)", got)
			}
			for _, want := range []string{"Iris", "backend-engineer", "app: hrn:app:acme.com:eng-team — Eng Team", "owner: acme.com", "working state: not visible to you"} {
				if !strings.Contains(out, want) {
					t.Errorf("profile output missing %q:\n%s", want, out)
				}
			}
			// Masked working state is omitted, never rendered as empty/free.
			for _, bad := range []string{"held by", "driven", "check status", "last team-channel check", "boot prompt", "reports to", "memory:", "family:", "Signature template", "Trailer template", "nobody", "never"} {
				if strings.Contains(out, bad) {
					t.Errorf("profile output must not carry %q:\n%s", bad, out)
				}
			}
		})
	}
}

func TestWorkerGetProfileJSONKeepsMasksNull(t *testing.T) {
	out, _, err := runWorkerGet(t, profileWorkerJSON, profileNames, "hrn:worker:acme.com:eng-team:iris", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if dto["appName"] != "Eng Team" || dto["ownerName"] != "acme.com" {
		t.Errorf("appName/ownerName = %v/%v", dto["appName"], dto["ownerName"])
	}
	for _, k := range []string{"hasLiveSession", "prompt", "memoryId", "checkStatus", "createdBy"} {
		if v, ok := dto[k]; !ok || v != nil {
			t.Errorf("%s must stay a present null on a profile read, got %v (present=%v)", k, v, ok)
		}
	}
}

func TestWorkerGetParticipantAddsNamesAndKeepsTheRest(t *testing.T) {
	live := withLiveness(irisWorkerJSON, "false")
	out, log, err := runWorkerGet(t, live, profileNames, "wkr1")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The App line is built from the scalars: no second App lookup.
	if log.count("TeamAppIdentity") != 0 {
		t.Errorf("participant read must not look the App up again: %s", log)
	}
	for _, want := range []string{"app: hrn:app:acme.com:eng-team — Eng Team", "owner: acme.com", "driven:", "held by: nobody", "boot prompt:", "You are Iris."} {
		if !strings.Contains(out, want) {
			t.Errorf("participant output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "working state: not visible") {
		t.Errorf("a participant read is not a profile read:\n%s", out)
	}
	js, _, err := runWorkerGet(t, live, profileNames, "wkr1", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto map[string]any
	_ = json.Unmarshal([]byte(js), &dto)
	if dto["appName"] != "Eng Team" || dto["ownerName"] != "acme.com" || dto["hasLiveSession"] != false {
		t.Errorf("participant JSON = %v", dto)
	}
}

func TestWorkerGetRetiredProfile(t *testing.T) {
	retired := strings.Replace(profileWorkerJSON, `"retiredAt":null`, `"retiredAt":"2026-09-01T00:00:00Z"`, 1)
	out, _, err := runWorkerGet(t, retired, profileNames, "hrn:worker:acme.com:eng-team:iris")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "retired: 2026-09-01T00:00:00Z") {
		t.Errorf("retired profile must say so:\n%s", out)
	}
}

func TestWorkerGetMissingNamesAreUnavailableNotAbsent(t *testing.T) {
	out, _, err := runWorkerGet(t, profileWorkerJSON, profileNamesNA, "hrn:worker:acme.com:eng-team:iris")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out, "owner:") {
		t.Errorf("an unavailable owner must print no owner line:\n%s", out)
	}
	if !strings.Contains(out, "app: capp100000000000000000000") {
		t.Errorf("without a name the App line falls back to the id:\n%s", out)
	}
	js, _, _ := runWorkerGet(t, profileWorkerJSON, profileNamesNA, "hrn:worker:acme.com:eng-team:iris", "--json")
	var dto map[string]any
	_ = json.Unmarshal([]byte(js), &dto)
	if v, ok := dto["appName"]; !ok || v != nil {
		t.Errorf("appName must be a present null, got %v (present=%v)", v, ok)
	}
	if v, ok := dto["ownerName"]; !ok || v != nil {
		t.Errorf("ownerName must be a present null, got %v (present=%v)", v, ok)
	}
}

func TestWorkerGetOldServerKeepsTodaysOutput(t *testing.T) {
	out, log, err := runWorkerGet(t, withLiveness(irisWorkerJSON, "false"), oldServerNames, "wkr1")
	if err != nil {
		t.Fatalf("an old server must not break the ordinary lookup: %v\n%s", err, out)
	}
	if log.count("TeamAppIdentity") != 1 {
		t.Errorf("old-server path keeps today's App lookup: %s", log)
	}
	if !strings.Contains(out, "app: hrn:app:acme.com:eng-team — Eng Team") || strings.Contains(out, "owner:") {
		t.Errorf("old-server output must be today's:\n%s", out)
	}
	js, _, _ := runWorkerGet(t, irisWorkerJSON, oldServerNames, "wkr1", "--json")
	var dto map[string]any
	_ = json.Unmarshal([]byte(js), &dto)
	if v, ok := dto["appName"]; !ok || v != nil {
		t.Errorf("old-server JSON appName must be a present null, got %v", v)
	}
}

// Only a documented capability absence falls back; anything else is an error,
// not a quietly old-looking success.
func TestWorkerGetNamesErrorsPropagate(t *testing.T) {
	for _, tc := range []struct{ name, resp string }{
		{"forbidden", `{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}]}`},
		{"unrelated validation", `{"errors":[{"message":"Cannot query field \"other\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`},
		{"server error", `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := runWorkerGet(t, profileWorkerJSON, tc.resp, "hrn:worker:acme.com:eng-team:iris")
			if err == nil {
				t.Fatalf("a names-read failure must surface, got success:\n%s", out)
			}
			if strings.Contains(out, "Iris") {
				t.Errorf("no partial success output on failure:\n%s", out)
			}
		})
	}
}
