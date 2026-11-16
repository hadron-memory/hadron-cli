package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Two rows share family and model (reported and not), a row with no findings
// and no approving reviews carries the nulls, and the not-recorded row is
// served FIRST here to prove the table puts it last.
const reviewStatsReport2190 = `{"data":{"reviewQualityReport":{"month":"2026-10","asOf":"2026-11-02T10:00:00.000Z","rows":[
{"recorded":false,"family":null,"model":null,"modelReported":null,"reviews":2,"findings":1,"findingsPerReview":0.5,"confirmedFindings":0,"rejectedFindings":1,"undisposedFindings":0,"shareConfirmed":0,"approvingReviews":1,"defectsMissed":0,"defectsMissedPerApprovingReview":0},
{"recorded":true,"family":"claude","model":"opus-5-5","modelReported":true,"reviews":4,"findings":6,"findingsPerReview":1.5,"confirmedFindings":3,"rejectedFindings":2,"undisposedFindings":1,"shareConfirmed":0.5,"approvingReviews":3,"defectsMissed":1,"defectsMissedPerApprovingReview":0.3333},
{"recorded":true,"family":"claude","model":"opus-5-5","modelReported":false,"reviews":1,"findings":0,"findingsPerReview":0,"confirmedFindings":0,"rejectedFindings":0,"undisposedFindings":0,"shareConfirmed":null,"approvingReviews":0,"defectsMissed":0,"defectsMissedPerApprovingReview":null}
]}}}`

func TestReviewStatsTable2190(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"TeamReviewQualityReport": reviewStatsReport2190, "TeamAppIdentity": teamAppIdentityJSON}, "team", "review-stats", "--month", "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	got := callsFor(calls, "TeamReviewQualityReport")
	if len(got) != 1 || got[0].Vars["month"] != "2026-10" || got[0].Vars["appRef"] != "capp100000000000000000000" || got[0].Session != "" {
		t.Fatalf("calls: %+v", got)
	}
	for _, want := range []string{"month: 2026-10 (UTC)", "as of: 2026-11-02T10:00:00.000Z", "can still change"} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q:\n%s", want, out)
		}
	}
	var rows []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "claude") || strings.HasPrefix(l, "not recorded") {
			rows = append(rows, l)
		}
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows:\n%s", out)
	}
	// reported and unreported rows of one family and model are distinct,
	// labelled rows; the not-recorded row is last, labelled, under no family.
	if f := strings.Fields(rows[0]); f[2] != "yes" || f[len(f)-1] != "0.33" {
		t.Errorf("reported row: %q", rows[0])
	}
	if f := strings.Fields(rows[1]); f[2] != "no" {
		t.Errorf("unreported row: %q", rows[1])
	}
	if !strings.HasPrefix(rows[2], "not recorded") {
		t.Errorf("last row: %q", rows[2])
	}
	// The three-way split and the integer defects print beside findings.
	if f := strings.Fields(rows[0]); strings.Join(f[3:12], " ") != "4 6 1.50 3 2 1 0.50 3 1" {
		t.Errorf("columns: %q", rows[0])
	}
	// No findings: share confirmed is empty, not 0. No approving reviews: the
	// per-approving ratio is empty, though defectsMissed prints 0.
	if f := strings.Fields(rows[1]); strings.Join(f[3:], " ") != "1 0 0.00 0 0 0 0 0" {
		t.Errorf("null ratios must print empty, never 0: %q", rows[1])
	}
}

func TestReviewStatsJSON2190(t *testing.T) {
	_, out, err := runTeamTask(t, map[string]string{"TeamReviewQualityReport": reviewStatsReport2190, "TeamAppIdentity": teamAppIdentityJSON}, "team", "review-stats", "--month", "2026-10", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Month, AsOf string
		Rows        []map[string]any
	}
	if json.Unmarshal([]byte(out), &d) != nil || d.Month != "2026-10" || d.AsOf == "" || len(d.Rows) != 3 {
		t.Fatal(out)
	}
	if len(d.Rows[0]) != 14 {
		t.Errorf("a row carries exactly the server's fields: %v", d.Rows[0])
	}
	if _, bad := d.Rows[0]["defectsMissedUnavailableReason"]; bad {
		t.Error("no unavailable-reason field")
	}
	for _, k := range []string{"family", "model", "modelReported"} {
		if v, ok := d.Rows[0][k]; !ok || v != nil {
			t.Errorf("%s must stay null on the not-recorded row: %v", k, d.Rows[0])
		}
	}
	if v, ok := d.Rows[2]["shareConfirmed"]; !ok || v != nil {
		t.Errorf("shareConfirmed null with no findings: %v", d.Rows[2])
	}
	if v, ok := d.Rows[2]["defectsMissedPerApprovingReview"]; !ok || v != nil {
		t.Errorf("per-approving null with no approving review: %v", d.Rows[2])
	}
	if d.Rows[2]["defectsMissed"] != float64(0) {
		t.Errorf("defectsMissed is the integer 0: %v", d.Rows[2])
	}
	if d.Rows[1]["modelReported"] != true || d.Rows[2]["modelReported"] != false {
		t.Errorf("modelReported: %v / %v", d.Rows[1], d.Rows[2])
	}
}

func TestReviewStatsNoRows2190(t *testing.T) {
	empty := `{"data":{"reviewQualityReport":{"month":"2026-01","asOf":"2026-02-01T00:00:00.000Z","rows":[]}}}`
	_, out, err := runTeamTask(t, map[string]string{"TeamReviewQualityReport": empty, "TeamAppIdentity": teamAppIdentityJSON}, "team", "review-stats", "--month", "2026-01", "--json")
	if err != nil || !strings.Contains(out, `"rows": []`) {
		t.Fatalf("%v %s", err, out)
	}
	_, out, err = runTeamTask(t, map[string]string{"TeamReviewQualityReport": empty, "TeamAppIdentity": teamAppIdentityJSON}, "team", "review-stats", "--month", "2026-01")
	if err != nil || !strings.Contains(out, "no reviews in this month") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestReviewStatsRefusals2190(t *testing.T) {
	for _, month := range []string{"", "2026-13", "2026-00", "2026-1", "26-10", "2026/10", "2026-10-01", " 2026-10"} {
		calls, _, err := runTeamTask(t, map[string]string{}, "team", "review-stats", "--month", month)
		if exitOf(err) != exitcode.Usage || len(*calls) != 0 {
			t.Errorf("month %q: exit %d, %d calls (err %v)", month, exitOf(err), len(*calls), err)
		}
	}
	// --month is required: omitting it is a usage error before any request.
	calls, _, err := runTeamTask(t, map[string]string{}, "team", "review-stats")
	if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
		t.Errorf("no --month: exit %d, %d calls (err %v)", exitCodeFor(err), len(*calls), err)
	}

	// No --app, no App context, no binding: exit 2 naming --app, no request.
	srv, seen := attnServer(t, map[string]string{})
	f, _ := testFactory(t)
	t.Chdir(t.TempDir())
	t.Setenv(team.GitDirEnv, "")
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "review-stats", "--month", "2026-10"})
	err = root.Execute()
	if exitOf(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "--app") || len(*seen) != 0 {
		t.Fatalf("no App: exit %d, %d calls, err %v", exitOf(err), len(*seen), err)
	}
}

// A non-reader and a missing App get the same server answer, and so the same
// exit and message (050:21): the CLI adds nothing that tells them apart.
func TestReviewStatsNotAReader2190(t *testing.T) {
	refusal := map[string]string{"TeamReviewQualityReport": gqlErrorJSON("APP_NOT_FOUND")}
	var msgs []string
	for range 2 {
		_, _, err := runTeamTask(t, refusal, "team", "review-stats", "--month", "2026-10")
		if exitOf(err) != exitcode.NotFound {
			t.Fatalf("exit %d: %v", exitOf(err), err)
		}
		msgs = append(msgs, err.Error())
	}
	if msgs[0] != msgs[1] {
		t.Fatalf("not byte-identical: %q vs %q", msgs[0], msgs[1])
	}
}

func TestReviewStatsAppFlagWins2190(t *testing.T) {
	calls, _, err := runTeamTask(t, map[string]string{"TeamReviewQualityReport": reviewStatsReport2190, "TeamAppIdentity": teamAppIdentityJSON}, "team", "review-stats", "--month", "2026-10", "--app", "acme.com:eng", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if got := callsFor(calls, "TeamReviewQualityReport"); len(got) != 1 || got[0].Vars["appRef"] != "hrn:app:acme.com:eng" {
		t.Fatalf("%+v", got)
	}
}

// A success envelope with no selected report (null, or data without the field)
// is an ordinary error, never a nil dereference (#2198 BLIND P2, LOCAL F1).
func TestReviewStatsMissingReport2190(t *testing.T) {
	for name, body := range map[string]string{
		"null report": `{"data":{"reviewQualityReport":null}}`,
		"empty data":  `{"data":{}}`,
		"null data":   `{"data":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := runTeamTask(t, map[string]string{"TeamReviewQualityReport": body, "TeamAppIdentity": teamAppIdentityJSON}, "team", "review-stats", "--month", "2026-10")
			if err == nil || exitOf(err) != exitcode.Error || !strings.Contains(err.Error(), "no review-quality report") {
				t.Fatalf("exit %d, err %v", exitOf(err), err)
			}
		})
	}
}
