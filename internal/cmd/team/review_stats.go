package team

import (
	"fmt"
	"io"
	"regexp"
	"strconv"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

var reviewStatsMonth = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

// reviewStatsRowDTO is one row of the --json document: exactly the server's
// fields, nulls kept as null (hadron-cli#2190).
type reviewStatsRowDTO struct {
	Recorded                        bool     `json:"recorded"`
	Family                          *string  `json:"family"`
	Model                           *string  `json:"model"`
	ModelReported                   *bool    `json:"modelReported"`
	Reviews                         int      `json:"reviews"`
	Findings                        int      `json:"findings"`
	FindingsPerReview               float64  `json:"findingsPerReview"`
	ConfirmedFindings               int      `json:"confirmedFindings"`
	RejectedFindings                int      `json:"rejectedFindings"`
	UndisposedFindings              int      `json:"undisposedFindings"`
	ShareConfirmed                  *float64 `json:"shareConfirmed"`
	ApprovingReviews                int      `json:"approvingReviews"`
	DefectsMissed                   int      `json:"defectsMissed"`
	DefectsMissedPerApprovingReview *float64 `json:"defectsMissedPerApprovingReview"`
}

type reviewStatsDTO struct {
	Month string              `json:"month"`
	AsOf  string              `json:"asOf"`
	Rows  []reviewStatsRowDTO `json:"rows"`
}

const reviewStatsHelp = `Read an App's review-quality measures for one UTC calendar month: one row
per reviewer model family and model, from the server's report. Read only, descriptive only:
the numbers never decide readiness or matching.

--month YYYY-MM is required and is a UTC month; anything else exits 2 before
any request. The App is --app, otherwise the App context (the configured App
or the worktree binding); with neither the command exits 2 before any
request. Readers are the App's participants and the App's own key; any other
caller gets the server's missing-App answer, exit 4, the same as for an App
that does not exist.

Rows are keyed (family, model, reported): a model the session reported and one
it did not are separate rows, so two rows with the same family and model are
not duplicates. The not-recorded row (reviews whose verdict carries no model
family or model) prints last and is never counted under a family.

Every ratio sits beside its numerator and denominator: findingsPerReview =
findings / reviews; shareConfirmed = confirmedFindings / findings (empty when
findings is 0); defectsMissedPerApprovingReview = defectsMissed /
approvingReviews (empty only when approvingReviews is 0). Findings split three
ways, none folded into another: confirmed (fixed, accepted, deferred),
rejected, and undisposed (no disposition yet). A past month's numbers can
still change, because findings and dispositions recorded later still count in
it; asOf says when they were worked out.

--json prints {month, asOf, rows}; rows is [] when the month has no reviews,
and a null field stays null.`

func newCmdTeamReviewStats(f *cmdutil.Factory) *cobra.Command {
	var month string
	c := &cobra.Command{
		Use:     "review-stats --month YYYY-MM",
		Short:   "Show an App's review-quality measures for one month",
		Long:    reviewStatsHelp,
		Example: "  hadron team review-stats --month 2026-10\n  hadron team review-stats --month 2026-10 --app acme.com:eng --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !reviewStatsMonth.MatchString(month) {
				return exitcode.Newf(exitcode.Usage, "--month must be a UTC calendar month written YYYY-MM (for example 2026-10), got %q", month)
			}
			// The App is --app, else the App context or the worktree binding;
			// with none, exit 2 naming --app before any request (a review
			// report belongs to an App, not a worktree, as for a role).
			scope, err := roleAppScope(cmd, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			r, err := gen.TeamReviewQualityReport(cmd.Context(), client, scope.Ref, month)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if r == nil || r.ReviewQualityReport == nil {
				return exitcode.Newf(exitcode.Error, "server returned no review-quality report")
			}
			rep := r.ReviewQualityReport
			d := reviewStatsDTO{Month: rep.Month, AsOf: rep.AsOf, Rows: []reviewStatsRowDTO{}}
			for _, w := range rep.Rows {
				d.Rows = append(d.Rows, reviewStatsRowDTO{
					Recorded: w.Recorded, Family: w.Family, Model: w.Model, ModelReported: w.ModelReported,
					Reviews: w.Reviews, Findings: w.Findings, FindingsPerReview: w.FindingsPerReview,
					ConfirmedFindings: w.ConfirmedFindings, RejectedFindings: w.RejectedFindings,
					UndisposedFindings: w.UndisposedFindings, ShareConfirmed: w.ShareConfirmed,
					ApprovingReviews: w.ApprovingReviews, DefectsMissed: w.DefectsMissed,
					DefectsMissedPerApprovingReview: w.DefectsMissedPerApprovingReview,
				})
			}
			label := lazyAppLabel(cmd.Context(), f, scope)
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				return writeReviewStats(w, label(), d)
			})
		},
	}
	c.Flags().StringVar(&month, "month", "", "the UTC calendar month, YYYY-MM (required)")
	return c
}

func writeReviewStats(w io.Writer, app string, d reviewStatsDTO) error {
	if _, err := fmt.Fprintf(w, "app: %s\nmonth: %s (UTC)\nas of: %s (a past month's numbers can still change)\n",
		app, output.SafeCell(d.Month), output.SafeCell(d.AsOf)); err != nil {
		return err
	}
	if len(d.Rows) == 0 {
		_, err := fmt.Fprintln(w, "no reviews in this month")
		return err
	}
	t := output.NewTable(w, "FAMILY", "MODEL", "REPORTED", "REVIEWS", "FINDINGS", "PER REVIEW",
		"CONFIRMED", "REJECTED", "UNDISPOSED", "SHARE CONFIRMED", "APPROVING", "DEFECTS MISSED", "PER APPROVING")
	// The server puts the not-recorded row last; keep it there whatever order
	// arrives, as its own labelled row and never under a family.
	for _, recorded := range []bool{true, false} {
		for _, r := range d.Rows {
			if r.Recorded != recorded {
				continue
			}
			family, model, reported := "not recorded", "—", "—"
			if r.Recorded {
				family, model = output.SafeCell(strOrDash(r.Family)), output.SafeCell(strOrDash(r.Model))
				reported = "no"
				if r.ModelReported == nil {
					reported = "—"
				} else if *r.ModelReported {
					reported = "yes"
				}
			}
			t.Row(family, model, reported,
				strconv.Itoa(r.Reviews), strconv.Itoa(r.Findings), ratio(&r.FindingsPerReview),
				strconv.Itoa(r.ConfirmedFindings), strconv.Itoa(r.RejectedFindings), strconv.Itoa(r.UndisposedFindings),
				ratio(r.ShareConfirmed), strconv.Itoa(r.ApprovingReviews), strconv.Itoa(r.DefectsMissed),
				ratio(r.DefectsMissedPerApprovingReview))
		}
	}
	return t.Flush()
}

func strOrDash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

// ratio prints a server ratio to two places; a null (no denominator) prints
// empty, never 0.
func ratio(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 2, 64)
}
