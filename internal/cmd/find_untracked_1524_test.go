package cmd

// hadron-cli#1524 C3: `team task find-untracked` over hadron-server#3125 S3
// (cor:agt:120:03). One call per invocation, every flag passed through, the
// server's bounds and refusals mapped; plan A3 for incomplete claims.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const untrackedItemJSON1524 = `{"externalId":"https://github.com/acme/app/issues/42","repository":"acme/app","number":42,
	"title":"Login flakes","url":"https://github.com/acme/app/issues/42","labels":["bug","team-marco"],
	"createdAt":"2026-10-01T00:00:00Z","updatedAt":"2026-10-02T00:00:00Z","linkedPullRequestMerged":false}`

const mergedItemJSON1524 = `{"externalId":"https://github.com/acme/app/issues/7","repository":"acme/app","number":7,
	"title":"Old bug","url":"https://github.com/acme/app/issues/7","labels":[],
	"createdAt":"2026-09-01T00:00:00Z","updatedAt":"2026-09-02T00:00:00Z","linkedPullRequestMerged":true}`

func untrackedPage1524(items string, total int, complete bool, reason string) string {
	r := "null"
	if reason != "" {
		r = `"` + reason + `"`
	}
	b, _ := json.Marshal(complete)
	return `{"data":{"untrackedTaskItems":{"items":[` + items + `],"total":` + itoa1524(total) + `,"complete":` + string(b) + `,"incompleteReason":` + r + `}}}`
}

func itoa1524(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

var claimedTaskJSON1524 = strings.NewReplacer(
	`"status":"PENDING","queued":true`, `"status":"STARTED","queued":false`,
	`"source":`, `"externalId":"https://github.com/acme/app/issues/42","source":`,
).Replace(taskFieldsJSON)

func claimResult1524(tasks string, complete bool, reason, refusal string) string {
	q := func(s string) string {
		if s == "" {
			return "null"
		}
		return `"` + s + `"`
	}
	b, _ := json.Marshal(complete)
	return `{"data":{"claimUntrackedTaskItems":{"tasks":[` + tasks + `],"complete":` + string(b) + `,"incompleteReason":` + q(reason) + `,"refusalCode":` + q(refusal) + `}}}`
}

func runFind1524(t *testing.T, responses map[string]string, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	calls, stdout, stderr, err := runTeamTaskAll1524(t, responses, append([]string{"team", "task", "find-untracked"}, argv...)...)
	return calls, stdout, stderr, err
}

func runTeamTaskAll1524(t *testing.T, responses map[string]string, argv ...string) (*[]attnCall, string, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

// findCalls1524 are the calls the command made, without the binding's App reads.
func findCalls1524(calls *[]attnCall) []attnCall {
	var got []attnCall
	for _, c := range *calls {
		if c.Op == "UntrackedTaskItems" || c.Op == "ClaimUntrackedTaskItems" {
			got = append(got, c)
		}
	}
	return got
}

func TestFindUntrackedList1524(t *testing.T) {
	page := map[string]string{"UntrackedTaskItems": untrackedPage1524(untrackedItemJSON1524+","+mergedItemJSON1524, 2, true, "")}

	t.Run("every flag passed through, one call", func(t *testing.T) {
		calls, _, _, err := runFind1524(t, page, "--integration", "github", "--team", "Marco", "--ready", "only",
			"--repo", "acme/app", "--skip", "40", "--limit", "500")
		if err != nil {
			t.Fatal(err)
		}
		got := findCalls1524(calls)
		if len(got) != 1 || got[0].Op != "UntrackedTaskItems" {
			t.Fatalf("calls = %v, want one UntrackedTaskItems", *calls)
		}
		v := got[0].Vars
		want := map[string]any{"integration": "github", "team": "Marco", "ready": "ONLY", "repository": "acme/app", "skip": float64(40), "limit": float64(500)}
		for k, w := range want {
			if v[k] != w {
				t.Errorf("%s = %#v, want %#v (sent unchanged: the server judges bounds)", k, v[k], w)
			}
		}
		if v["appRef"] == nil || v["appRef"] == "" {
			t.Errorf("appRef missing: %v", v)
		}
	})

	t.Run("omitted optional flags send no key", func(t *testing.T) {
		calls, _, _, err := runFind1524(t, page, "--integration", "github")
		if err != nil {
			t.Fatal(err)
		}
		v := findCalls1524(calls)[0].Vars
		for _, k := range []string{"team", "ready", "repository", "skip", "limit"} {
			if _, present := v[k]; present {
				t.Errorf("%s sent unasked: %v", k, v)
			}
		}
	})

	t.Run("json shape with arrays, and the close-check flag kept", func(t *testing.T) {
		_, out, _, err := runFind1524(t, page, "--integration", "github", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var dto struct {
			Items []struct {
				ExternalID              string   `json:"externalId"`
				Labels                  []string `json:"labels"`
				LinkedPullRequestMerged bool     `json:"linkedPullRequestMerged"`
			} `json:"items"`
			Total            int     `json:"total"`
			Complete         bool    `json:"complete"`
			IncompleteReason *string `json:"incompleteReason"`
		}
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if len(dto.Items) != 2 || dto.Total != 2 || !dto.Complete || dto.IncompleteReason != nil {
			t.Errorf("dto = %+v", dto)
		}
		if dto.Items[1].Labels == nil || !dto.Items[1].LinkedPullRequestMerged {
			t.Errorf("labels must be [] and the flag kept: %+v", dto.Items[1])
		}
		if !strings.Contains(out, `"labels": []`) {
			t.Errorf("empty labels must render [], not null:\n%s", out)
		}
	})

	t.Run("text shows the close-check column for a merged item", func(t *testing.T) {
		_, out, _, err := runFind1524(t, page, "--integration", "github")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"CLOSE CHECK", "merged PR: close check", "acme/app", "Login flakes", "bug,team-marco"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("an empty page is items []", func(t *testing.T) {
		_, out, _, err := runFind1524(t, map[string]string{"UntrackedTaskItems": untrackedPage1524("", 0, true, "")}, "--integration", "github", "--json")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `"items": []`) {
			t.Errorf("empty items must be []:\n%s", out)
		}
	})

	t.Run("an incomplete list exits 0 with a stderr note, stdout still JSON", func(t *testing.T) {
		r := map[string]string{"UntrackedTaskItems": untrackedPage1524(untrackedItemJSON1524, 30, false, "rate_limited")}
		_, out, stderr, err := runFind1524(t, r, "--integration", "github", "--json")
		if err != nil {
			t.Fatalf("an incomplete read is not a refusal: %v", err)
		}
		var any1 map[string]any
		if err := json.Unmarshal([]byte(out), &any1); err != nil {
			t.Fatalf("stdout must stay one JSON document: %v\n%s", err, out)
		}
		if !strings.Contains(stderr, "rate_limited") {
			t.Errorf("stderr note missing: %q", stderr)
		}
	})

	t.Run("title text is terminal-safe", func(t *testing.T) {
		evil := strings.Replace(untrackedItemJSON1524, "Login flakes", `Login\u001b[31m flakes\u202e`, 1)
		_, out, _, err := runFind1524(t, map[string]string{"UntrackedTaskItems": untrackedPage1524(evil, 1, true, "")}, "--integration", "github")
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(out, "\x1b\u202e") {
			t.Errorf("raw control/format characters reached the terminal: %q", out)
		}
	})
}

func TestFindUntrackedUsage1524(t *testing.T) {
	page := map[string]string{"UntrackedTaskItems": untrackedPage1524("", 0, true, "")}
	for name, argv := range map[string][]string{
		"no --integration":             {},
		"--assign-to-me with --skip":   {"--integration", "github", "--assign-to-me", "--skip", "2"},
		"--ready outside the enum":     {"--integration", "github", "--ready", "soon"},
		"a bare --team (no value)":     {"--integration", "github", "--team"},
		"an empty --integration value": {"--integration", " "},
	} {
		t.Run(name, func(t *testing.T) {
			calls, _, _, err := runFind1524(t, page, argv...)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Usage, err)
			}
			if strings.Contains(err.Error(), "unknown command") || strings.Contains(err.Error(), "unknown flag: --integration") {
				t.Fatalf("the command must exist: %v", err)
			}
			if len(findCalls1524(calls)) != 0 {
				t.Errorf("refused before any call: %v", *calls)
			}
		})
	}
}

func TestFindUntrackedRefusals1524(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{
		{"TASK_INTEGRATION_NOT_FOUND", exitcode.NotFound},
		{"TASK_INTEGRATION_REPOSITORY_NOT_CONNECTED", exitcode.Usage},
		{"TASK_INTEGRATION_TEAM_UNKNOWN", exitcode.Usage},
		{"TASK_INTEGRATION_ORG_REQUIRED", exitcode.Conflict},
		{"BAD_USER_INPUT", exitcode.Usage},
		{"GITHUB_NOT_INSTALLED", exitcode.Conflict},
		{"GITHUB_PERMISSION_MISSING", exitcode.Forbidden},
		{"GITHUB_REPOSITORY_NOT_ALLOWED", exitcode.Forbidden},
		{"GITHUB_TOKEN_SCOPE_MISMATCH", exitcode.Conflict},
		{"GITHUB_UNAVAILABLE", exitcode.Unavailable},
		{"GITHUB_INVALID_ARGUMENT", exitcode.Usage},
		{"TASK_CLAIM_PRINCIPAL_UNSUPPORTED", exitcode.Forbidden},
		{"APP_NOT_FOUND", exitcode.NotFound},
	} {
		for _, op := range []string{"UntrackedTaskItems", "ClaimUntrackedTaskItems"} {
			t.Run(tc.code+"/"+op, func(t *testing.T) {
				r := map[string]string{op: `{"errors":[{"message":"refused","extensions":{"code":"` + tc.code + `"}}]}`}
				argv := []string{"--integration", "github", "--json"}
				if op == "ClaimUntrackedTaskItems" {
					argv = append(argv, "--assign-to-me")
				}
				calls, out, _, err := runFind1524(t, r, argv...)
				if exitCodeFor(err) != tc.exit {
					t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), tc.exit, err)
				}
				if out != "" {
					t.Errorf("a thrown refusal prints nothing on stdout: %q", out)
				}
				if env := envelope1524(t, err); env["serverCode"] != tc.code {
					t.Errorf("serverCode = %v, want %s", env["serverCode"], tc.code)
				}
				if len(findCalls1524(calls)) != 1 {
					t.Errorf("exactly one call, never retried: %v", *calls)
				}
			})
		}
	}
}

func TestFindUntrackedClaim1524(t *testing.T) {
	t.Run("one claim call, flags through, no skip, omitted limit sends no key", func(t *testing.T) {
		r := map[string]string{"ClaimUntrackedTaskItems": claimResult1524(claimedTaskJSON1524, true, "", "")}
		calls, _, _, err := runFind1524(t, r, "--integration", "github", "--assign-to-me", "--team", "Marco", "--ready", "exclude", "--repo", "acme/app")
		if err != nil {
			t.Fatal(err)
		}
		got := findCalls1524(calls)
		if len(got) != 1 || got[0].Op != "ClaimUntrackedTaskItems" {
			t.Fatalf("calls = %v, want one ClaimUntrackedTaskItems (no preflight list)", *calls)
		}
		v := got[0].Vars
		if v["integration"] != "github" || v["team"] != "Marco" || v["ready"] != "EXCLUDE" || v["repository"] != "acme/app" {
			t.Errorf("vars = %v", v)
		}
		for _, k := range []string{"limit", "skip"} {
			if _, present := v[k]; present {
				t.Errorf("%s must not be sent: %v", k, v)
			}
		}
	})

	t.Run("--limit is passed through unchanged", func(t *testing.T) {
		r := map[string]string{"ClaimUntrackedTaskItems": claimResult1524(claimedTaskJSON1524, true, "", "")}
		calls, _, _, err := runFind1524(t, r, "--integration", "github", "--assign-to-me", "--limit", "50")
		if err != nil {
			t.Fatal(err)
		}
		if findCalls1524(calls)[0].Vars["limit"] != float64(50) {
			t.Errorf("limit = %v", findCalls1524(calls)[0].Vars["limit"])
		}
	})

	t.Run("prints the claimed tasks, STARTED with externalId", func(t *testing.T) {
		r := map[string]string{"ClaimUntrackedTaskItems": claimResult1524(claimedTaskJSON1524, true, "", "")}
		_, out, _, err := runFind1524(t, r, "--integration", "github", "--assign-to-me", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var dto struct {
			Tasks []struct {
				Status     string `json:"status"`
				ExternalID string `json:"externalId"`
			} `json:"tasks"`
			Complete         bool    `json:"complete"`
			IncompleteReason *string `json:"incompleteReason"`
			RefusalCode      *string `json:"refusalCode"`
		}
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if len(dto.Tasks) != 1 || dto.Tasks[0].Status != "STARTED" || dto.Tasks[0].ExternalID != "https://github.com/acme/app/issues/42" || !dto.Complete {
			t.Errorf("dto = %+v", dto)
		}
		_, out, _, _ = runFind1524(t, r, "--integration", "github", "--assign-to-me")
		if !strings.Contains(out, "https://github.com/acme/app/issues/42") || !strings.Contains(out, "STARTED") {
			t.Errorf("text must list the claimed task:\n%s", out)
		}
	})

	t.Run("an empty claim is tasks [] and exit 0", func(t *testing.T) {
		r := map[string]string{"ClaimUntrackedTaskItems": claimResult1524("", true, "", "")}
		_, out, _, err := runFind1524(t, r, "--integration", "github", "--assign-to-me", "--json")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `"tasks": []`) {
			t.Errorf("empty tasks must be []:\n%s", out)
		}
	})

	t.Run("claim_refused prints the tasks then exits with the refusal's code", func(t *testing.T) {
		r := map[string]string{"ClaimUntrackedTaskItems": claimResult1524(claimedTaskJSON1524, false, "claim_refused", "GITHUB_PERMISSION_MISSING")}
		_, out, stderr, err := runFind1524(t, r, "--integration", "github", "--assign-to-me", "--limit", "3", "--json")
		if exitCodeFor(err) != exitcode.Forbidden {
			t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Forbidden, err)
		}
		var dto map[string]any
		if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
			t.Fatalf("stdout must be the one result document: %v\n%s", jerr, out)
		}
		if tasks, _ := dto["tasks"].([]any); len(tasks) != 1 {
			t.Errorf("the claimed task must be on stdout: %s", out)
		}
		if env := envelope1524(t, err); env["serverCode"] != "GITHUB_PERMISSION_MISSING" {
			t.Errorf("serverCode = %v", env["serverCode"])
		}
		if !strings.Contains(err.Error(), "1 task") || !strings.Contains(err.Error(), "yours") {
			t.Errorf("the error must say how many were claimed and that they are yours: %v", err)
		}
		_ = stderr
	})

	t.Run("claim_refused with a null code still exits non-zero", func(t *testing.T) {
		r := map[string]string{"ClaimUntrackedTaskItems": claimResult1524(claimedTaskJSON1524, false, "claim_refused", "")}
		_, out, _, err := runFind1524(t, r, "--integration", "github", "--assign-to-me", "--json")
		if exitCodeFor(err) != exitcode.Error {
			t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Error, err)
		}
		if !strings.Contains(out, `"tasks"`) {
			t.Errorf("the tasks must still print:\n%s", out)
		}
	})

	t.Run("claim_failed prints the tasks and exits 1", func(t *testing.T) {
		r := map[string]string{"ClaimUntrackedTaskItems": claimResult1524(claimedTaskJSON1524, false, "claim_failed", "")}
		_, out, _, err := runFind1524(t, r, "--integration", "github", "--assign-to-me", "--json")
		if exitCodeFor(err) != exitcode.Error {
			t.Fatalf("exit = %d, want %d: %v", exitCodeFor(err), exitcode.Error, err)
		}
		if !strings.Contains(out, `"tasks"`) {
			t.Errorf("the tasks must still print:\n%s", out)
		}
	})

	t.Run("a claim cut short by the tracker exits 0 with a note", func(t *testing.T) {
		r := map[string]string{"ClaimUntrackedTaskItems": claimResult1524(claimedTaskJSON1524, false, "rate_limited", "")}
		_, out, stderr, err := runFind1524(t, r, "--integration", "github", "--assign-to-me", "--json")
		if err != nil {
			t.Fatalf("a tracker read cut short is not a refusal: %v", err)
		}
		var dto map[string]any
		if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
			t.Fatalf("stdout must be JSON: %v\n%s", jerr, out)
		}
		if !strings.Contains(stderr, "rate_limited") {
			t.Errorf("stderr note missing: %q", stderr)
		}
	})
}
