package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// These are literal public JSON values, deliberately independent of the
// producer's strings. A stage rename must fail the corresponding command pin.
func TestIntakeFailureStages2257(t *testing.T) {
	for _, track := range []bool{false, true} {
		mode := "list"
		if track {
			mode = "track"
		}
		for _, stage := range []string{"check_failed", "claim_failed", "walk_failed", "mark_failed", "reconcile_failed", "reconcile_over_limit", "limit_reached", "exhausted"} {
			if !track && (stage == "claim_failed" || stage == "mark_failed") {
				continue
			}
			t.Run(mode+"/"+stage, func(t *testing.T) {
				list := `echo '{"items":[` + item1961(itemA1961, "one") + `]}'`
				mark := ""
				responses := map[string]string{
					"IntakeTasksByExternalIds": held1961(),
					"IntakeClaimTask":          claimed1961("t1", itemA1961, "wkr1", "Iris", "", "STARTED"),
				}
				limit, wantCode, wantReason, wantCount := 3, exitcode.Error, stage, 0
				switch stage {
				case "check_failed":
					responses["IntakeTasksByExternalIds"] = `{"errors":[{"message":"check refused","extensions":{"code":"BAD_USER_INPUT"}}]}`
					wantCode = exitcode.Usage
				case "claim_failed":
					responses["IntakeClaimTask"] = `{"errors":[{"message":"claim refused","extensions":{"code":"TASK_ASSIGNEE_INVALID"}}]}`
					wantCode = exitcode.Usage
				case "walk_failed":
					list = `printf '%s' 'not-json'`
				case "mark_failed":
					mark = `exit 4`
					wantCount = 1
				case "reconcile_failed", "reconcile_over_limit":
					// Only the old task's write-back fails. A new claim can still mark,
					// independently proving that the list/claim walk reaches its limit.
					mark = `case "$req" in *'old-task'*) exit 4;; *) echo '{}';; esac`
					responses["IntakeExternalIdChanges"] = feed1962(false, "", change1962("REASSIGNED", itemR1, "c1", "old-task", "AT0.old", "wkr9", "Gus"))
					if stage == "reconcile_failed" {
						list = `echo '{"items":[]}'`
					} else {
						limit = 1
						wantReason = "reconcile_failed"
						wantCount = 1
					}
				case "limit_reached":
					limit = 1
					wantCode = 0
					wantCount = 1
				case "exhausted":
					wantCode = 0
					wantReason = ""
					wantCount = 1
				}
				path, log := fakeAdapter1961(t, "", list, mark)
				args := []string{"--json", "--limit", fmt.Sprint(limit)}
				if track {
					args = append(args, "--track")
				}
				calls, out, _, err := run1961(t, path, responses, args...)
				if got := exitCodeFor(err); got != wantCode {
					t.Fatalf("exit=%d want=%d err=%v output=%s", got, wantCode, err, out)
				}
				var receipt struct {
					Complete      *bool   `json:"complete"`
					StoppedReason *string `json:"stoppedReason"`
					Items         []struct {
						ExternalID string `json:"externalId"`
					} `json:"items"`
					Tasks []struct {
						ExternalID string `json:"externalId"`
						Marked     bool   `json:"marked"`
					} `json:"tasks"`
					Reconcile struct {
						Complete bool    `json:"complete"`
						Error    *string `json:"error"`
					} `json:"reconcile"`
				}
				if err := json.Unmarshal([]byte(out), &receipt); err != nil {
					t.Fatalf("receipt: %v: %s", err, out)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal([]byte(out), &fields); err != nil {
					t.Fatal(err)
				}
				if _, present := fields["stoppedReason"]; !present {
					t.Fatal("stoppedReason omitted")
				}
				if receipt.Complete == nil || *receipt.Complete != (stage == "exhausted") {
					t.Fatalf("failure/limit confused with exhaustion: %s", out)
				}
				if wantReason == "" {
					if receipt.StoppedReason != nil {
						t.Fatalf("exhaustion reason must be null: %s", out)
					}
				} else if receipt.StoppedReason == nil || *receipt.StoppedReason != wantReason {
					t.Fatalf("want stoppedReason=%q: %s", wantReason, out)
				}
				if track {
					if receipt.Tasks == nil || len(receipt.Tasks) != wantCount {
						t.Fatalf("wrong claims in partial receipt: %s", out)
					}
					if wantCount > 0 && (receipt.Tasks[0].ExternalID != itemA1961 || receipt.Tasks[0].Marked != (stage != "mark_failed")) {
						t.Fatalf("held claim lost or misreported: %s", out)
					}
				} else if receipt.Items == nil || len(receipt.Items) != wantCount {
					t.Fatalf("wrong list in partial receipt: %s", out)
				}
				if strings.HasPrefix(stage, "reconcile_") {
					if receipt.Reconcile.Complete || receipt.Reconcile.Error == nil {
						t.Fatalf("reconcile failure hidden: %s", out)
					}
					if len(adapterLog1961(t, log, "list-untracked")) != 1 {
						t.Fatal("reconcile failure suppressed the walk")
					}
				}
				if stage == "reconcile_over_limit" && track {
					if len(opsOf1961(calls, "IntakeClaimTask")) != 1 || len(adapterLog1961(t, log, "mark-tracked")) != 2 {
						t.Fatal("limit must really be reached after the failed old-task mark")
					}
				}
				if stage == "check_failed" || stage == "walk_failed" {
					if len(opsOf1961(calls, "IntakeClaimTask")) != 0 {
						t.Fatal("failed pre-claim stage must not create a task")
					}
				}
			})
		}
	}
}
