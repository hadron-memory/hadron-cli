package team

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

var prSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var prSlotAtom = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9_.-]*[A-Za-z0-9])?$`)

type prTaskDTO struct {
	ID     string `json:"id"`
	Handle string `json:"handle"`
	Status string `json:"status"`
}
type prReceiptDTO struct {
	Task   prTaskDTO               `json:"task"`
	Action cmdutil.ActionAnswerDTO `json:"action"`
}
type prSlotDTO struct {
	Slot          string  `json:"slot"`
	CurrentTaskID *string `json:"currentTaskId"`
	State         string  `json:"state"`
	Verdict       *string `json:"verdict"`
	Head          *string `json:"head"`
	Counts        bool    `json:"counts"`
}
type prReadinessDTO struct {
	Ready bool        `json:"ready"`
	Head  string      `json:"head"`
	Slots []prSlotDTO `json:"slots"`
}

func validPRSlot(slot string) bool {
	if len(slot) > 64 {
		return false
	}
	for _, atom := range strings.Split(slot, ":") {
		if !prSlotAtom.MatchString(atom) {
			return false
		}
	}
	return true
}
func newCmdTeamPR(f *cmdutil.Factory) *cobra.Command {
	parent := &cobra.Command{Use: "pr <command>", Short: "Record PR review facts and read readiness at an explicit head", Long: `Record the server's PR review protocol, or read its event-derived readiness.
Requires a server containing hadron-server#3448 (merged at 06f4b59c).
Every task reference accepts the same id, handle or URN forms as team task get.
A bare task handle may need qualification when several Apps are visible.
Owner chain means ` + taskAssignerDoc + `. The assignee is the person or worker assigned to that task.
The server alone judges owner-chain/assignee authority and readiness. These
commands do not validate model families, merge a PR, or finish a seat task.

The five writes append review.scheduled, pr.frozen, review.seat.assigned,
review.verdict and pr.ready respectively. Inspect them with team events --type.
Bound writes use verified worker attribution, as team task does; otherwise
they act as the authenticated person. An App key may read readiness only.

Each command makes one protocol request, with no head inference, property-
mirror read, retry or automatic follow-up. If a write's outcome is unknown,
inspect task/events before repeating it. Readiness is the server's slot view,
not a GitHub CI or merge-authority check.

A write prints the task handle/id/status and register code/state/loc/URN;
--json is {task:{id,handle,status},action:{code,state,taskLoc,taskUrn}}. A missing
loc or URN is null in JSON and a dash in text. Readiness prints READY or NOT
READY and each scheduled slot, seat, state, verdict, head and whether it counts;
--json is {ready,head,slots:[{slot,currentTaskId,state,verdict,head,counts}]}.
NOT READY is a successful query, exit 0; a refused ready write is exit 5.

Optional node/receipt evidence is recorded as text in events/properties, not
fetched or verified as evidence. Put no secrets in these references. Protocol
conflicts exit 5, with serverCode in JSON; PR_NOT_READY also names failing
slots in text and error.slots in JSON. Unknown transport outcomes are exit 7.`}
	for _, verb := range []string{"schedule", "freeze", "seat", "verdict", "readiness", "ready"} {
		var head, slot, supersedes, verdict, node, receiptURL string
		var slots []string
		uses := map[string]string{"schedule": "schedule <build-task> --slot <id> [--slot <id>...]", "freeze": "freeze <build-task> <40-hex-sha>", "seat": "seat <seat-task> --slot <id> --head <sha>", "verdict": "verdict <seat-task> --head <sha> --verdict <verdict>", "readiness": "readiness <build-task> --head <sha>", "ready": "ready <build-task> --head <sha>"}
		shorts := map[string]string{"schedule": "Replace the review schedule (owner chain only)", "freeze": "Freeze a full head SHA (build assignee only)", "seat": "Bind a child seat task to a scheduled slot (owner chain only)", "verdict": "Record a STARTED seat's verdict without finishing it (seat assignee only)", "readiness": "Read event-derived readiness for the required explicit head", "ready": "Record a ready call only when the server says every slot counts"}
		c := &cobra.Command{Use: uses[verb], Short: shorts[verb], Long: shorts[verb] + ".\n\n" + parent.Long, Args: cobra.ExactArgs(1)}
		if verb == "freeze" {
			c.Args = cobra.ExactArgs(2)
		}
		c.RunE = func(cmd *cobra.Command, args []string) error {
			task := strings.TrimSpace(args[0])
			if task == "" {
				return exitcode.Newf(exitcode.Usage, "task reference must not be blank")
			}
			if verb == "freeze" {
				head = args[1]
			}
			if verb != "schedule" && !prSHA.MatchString(head) {
				return exitcode.Newf(exitcode.Usage, "head must be a full 40-hex SHA")
			}
			if verb == "schedule" {
				if len(slots) == 0 {
					return exitcode.Newf(exitcode.Usage, "name at least one --slot")
				}
				seen := map[string]bool{}
				for _, v := range slots {
					if !validPRSlot(v) || seen[v] {
						return exitcode.Newf(exitcode.Usage, "slots must be unique colon-separated atoms, at most 64 characters each")
					}
					seen[v] = true
				}
			}
			if verb == "seat" && !validPRSlot(slot) {
				return exitcode.Newf(exitcode.Usage, "slot must be colon-separated atoms, at most 64 characters")
			}
			if verb == "verdict" {
				switch verdict {
				case "APPROVE", "CHANGES_REQUESTED", "PASS", "FAIL", "CARRY", "REFUSED":
				default:
					return exitcode.Newf(exitcode.Usage, "verdict must be APPROVE, CHANGES_REQUESTED, PASS, FAIL, CARRY or REFUSED")
				}
			}
			optional := func(name, value string) (*string, error) {
				if !cmd.Flags().Changed(name) {
					return nil, nil
				}
				if strings.TrimSpace(value) == "" {
					return nil, exitcode.Newf(exitcode.Usage, "--%s must not be blank", name)
				}
				return &value, nil
			}
			sup, err := optional("supersedes", supersedes)
			if err != nil {
				return err
			}
			n, err := optional("node", node)
			if err != nil {
				return err
			}
			url, err := optional("receipt", receiptURL)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if verb == "readiness" {
				r, e := gen.PRReadiness(cmd.Context(), client, task, head)
				if e != nil {
					return api.MapPRProtocolError(e)
				}
				if r == nil || r.PullRequestReadiness == nil {
					return exitcode.Newf(exitcode.NotFound, "task not found or not readable")
				}
				v := r.PullRequestReadiness
				dto := prReadinessDTO{Ready: v.Ready, Head: v.Head, Slots: []prSlotDTO{}}
				for _, s := range v.Slots {
					if s == nil {
						return exitcode.Newf(exitcode.Unavailable, "server returned an incomplete readiness slot")
					}
					var verdict *string
					if s.Verdict != nil {
						value := string(*s.Verdict)
						verdict = &value
					}
					dto.Slots = append(dto.Slots, prSlotDTO{Slot: s.Slot, CurrentTaskID: s.CurrentTaskId, State: s.State, Verdict: verdict, Head: s.Head, Counts: s.Counts})
				}
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					state := "NOT READY"
					if dto.Ready {
						state = "READY"
					}
					if _, e := fmt.Fprintf(w, "%s  head %s\n", state, output.SafeCell(dto.Head)); e != nil {
						return e
					}
					table := output.NewTable(w, "SLOT", "CURRENT SEAT", "STATE", "VERDICT", "HEAD", "COUNTS")
					for _, s := range dto.Slots {
						counts := "does not count"
						if s.Counts {
							counts = "counts"
						}
						table.Row(output.SafeCell(s.Slot), prCell(s.CurrentTaskID), output.SafeCell(s.State), prCell(s.Verdict), prCell(s.Head), counts)
					}
					return table.Flush()
				})
			}
			var result *gen.PRProtocolReceipt
			switch verb {
			case "schedule":
				r, e := gen.PRSchedule(cmd.Context(), client, task, slots)
				err = e
				if r != nil && r.SetReviewSchedule != nil {
					result = &r.SetReviewSchedule.PRProtocolReceipt
				}
			case "freeze":
				r, e := gen.PRFreeze(cmd.Context(), client, task, head)
				err = e
				if r != nil && r.FreezePullRequestHead != nil {
					result = &r.FreezePullRequestHead.PRProtocolReceipt
				}
			case "seat":
				r, e := gen.PRSeat(cmd.Context(), client, task, slot, head, sup)
				err = e
				if r != nil && r.AssignReviewSeat != nil {
					result = &r.AssignReviewSeat.PRProtocolReceipt
				}
			case "verdict":
				r, e := gen.PRVerdict(cmd.Context(), client, task, head, gen.ReviewVerdict(verdict), n, url)
				err = e
				if r != nil && r.RecordReviewVerdict != nil {
					result = &r.RecordReviewVerdict.PRProtocolReceipt
				}
			case "ready":
				r, e := gen.PRReady(cmd.Context(), client, task, head)
				err = e
				if r != nil && r.RecordReadyCall != nil {
					result = &r.RecordReadyCall.PRProtocolReceipt
				}
			}
			if err != nil {
				return api.MapPRProtocolError(err)
			}
			if result == nil || result.Task == nil || result.Action == nil || result.Task.Id == "" || result.Action.Code == "" || result.Action.State == "" {
				return exitcode.Newf(exitcode.Unavailable, "server returned no PR receipt; inspect task/events before repeating the command")
			}
			t, a := result.Task, result.Action
			dto := prReceiptDTO{Task: prTaskDTO{ID: t.Id, Handle: t.Handle, Status: string(t.Status)}, Action: cmdutil.ActionAnswerDTO{Code: a.Code, State: string(a.State), TaskLoc: a.TaskLoc, TaskURN: a.TaskUrn}}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "%s %s (%s)\naction %s  %s\ntask loc %s  task URN %s\n", output.SafeCell(dto.Task.Handle), output.SafeCell(dto.Task.ID), output.SafeCell(dto.Task.Status), output.SafeCell(dto.Action.Code), output.SafeCell(dto.Action.State), prCell(dto.Action.TaskLoc), prCell(dto.Action.TaskURN))
				return err
			})
		}
		switch verb {
		case "schedule":
			c.Flags().StringArrayVar(&slots, "slot", nil, "required unique slot id; repeat for each scheduled slot (colon-separated atoms, at most 64 characters)")
		case "freeze":
		default:
			c.Flags().StringVar(&head, "head", "", "required explicit full 40-hex SHA; never inferred")
		}
		if verb == "seat" {
			c.Flags().StringVar(&slot, "slot", "", "required scheduled slot id")
			c.Flags().StringVar(&supersedes, "supersedes", "", "current sibling seat task to replace; server enforces negative-verdict restrictions")
		}
		if verb == "verdict" {
			c.Flags().StringVar(&verdict, "verdict", "", "required APPROVE, CHANGES_REQUESTED, PASS, FAIL, CARRY or REFUSED")
			c.Flags().StringVar(&node, "node", "", "optional evidence node reference, recorded as text (not fetched)")
			c.Flags().StringVar(&receiptURL, "receipt", "", "optional receipt URL, recorded as text (not fetched)")
		}
		parent.AddCommand(c)
	}
	return parent
}
func prCell(value *string) string {
	if value == nil {
		return "—"
	}
	return output.SafeCell(*value)
}
