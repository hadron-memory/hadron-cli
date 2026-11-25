package team

import (
	"errors"
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
	parent := &cobra.Command{Use: "pr <command>", Short: "Record PR review facts and read readiness at an explicit head", Long: prGroupLong, Example: prGroupExample}
	for _, verb := range []string{"schedule", "freeze", "seat", "verdict", "withdraw", "readiness", "ready"} {
		var head, slot, supersedes, verdict, node, receiptURL, rawInstruction string
		var slots []string
		uses := map[string]string{"schedule": "schedule <build-task> --slot <id> [--slot <id>...]", "freeze": "freeze <build-task> <40-hex-sha>", "seat": "seat <seat-task> --slot <id> --head <sha>", "verdict": "verdict <seat-task> --head <sha> --verdict <verdict>", "withdraw": "withdraw <seat-task> --head <sha> [--receipt <url>] [--instruction <kind>:<ref>]", "readiness": "readiness <build-task> --head <sha>", "ready": "ready <build-task> --head <sha>"}
		shorts := map[string]string{"schedule": "Replace the review slots a PR needs (owner chain only)", "freeze": "Fix the head under review (build assignee only)", "seat": "Bind a seat task to a scheduled slot (the seat task's owner chain only)", "verdict": "Record a seat's verdict without finishing it (seat assignee only)", "withdraw": "Withdraw a finished seat's negative verdict at the same head (its reviewer or a coordinator)", "readiness": "Show whether every scheduled review counts at a head", "ready": "Record the ready call once every review counts (build assignee only)"}
		c := &cobra.Command{Use: uses[verb], Short: shorts[verb], Long: prLeafLong[verb], Example: prLeafExample[verb], Args: cobra.ExactArgs(1)}
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
			claim, err := taskClaim(cmd, rawInstruction)
			if err != nil {
				return err
			}
			// --app reaches every verb's task ref (it was ignored, cli#3037); readiness, a read, also
			// qualifies a bare handle from the worktree binding. The writes resolve through the worker
			// session they send and make no extra request.
			if verb == "readiness" {
				task, err = qualifyTaskRefFromBinding(cmd.Context(), f, task)
			} else {
				task, err = qualifyTaskRef(f, task)
			}
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
					return mapPRError(e)
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
			case "withdraw":
				// The claim rides only this write and the server judges it; a refusal is shown once and the
				// call is never resent without it.
				if claim != nil {
					r, e := gen.PRWithdrawInstructed(cmd.Context(), client, task, head, url, claim)
					err = e
					if r != nil && r.WithdrawReviewVerdict != nil {
						result = &r.WithdrawReviewVerdict.PRProtocolReceipt
					}
				} else {
					r, e := gen.PRWithdraw(cmd.Context(), client, task, head, url)
					err = e
					if r != nil && r.WithdrawReviewVerdict != nil {
						result = &r.WithdrawReviewVerdict.PRProtocolReceipt
					}
				}
			case "ready":
				r, e := gen.PRReady(cmd.Context(), client, task, head)
				err = e
				if r != nil && r.RecordReadyCall != nil {
					result = &r.RecordReadyCall.PRProtocolReceipt
				}
			}
			if err != nil {
				return mapPRError(err)
			}
			if result == nil || result.Task == nil || result.Action == nil || result.Task.Id == "" || result.Action.Code == "" || result.Action.State == "" {
				return exitcode.Newf(exitcode.Unavailable, "server returned no PR receipt; inspect task/events before repeating the command")
			}
			t, a := result.Task, result.Action
			dto := prReceiptDTO{Task: prTaskDTO{ID: t.Id, Handle: t.Handle, Status: string(t.Status)}, Action: cmdutil.ActionAnswerDTO{Code: a.Code, State: string(a.State), TaskLoc: a.TaskLoc, TaskURN: a.TaskUrn}}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "%s %s (%s)\n%s\n", output.SafeCell(dto.Task.Handle), output.SafeCell(dto.Task.ID), output.SafeCell(dto.Task.Status), prRegisterLine(dto.Action))
				return err
			})
		}
		switch verb {
		case "schedule":
			c.Flags().StringArrayVar(&slots, "slot", nil, "one required review, such as local, blind or qa; repeat for each (unique; colon-separated atoms, at most 64 characters)")
		case "freeze":
		default:
			c.Flags().StringVar(&head, "head", "", "the frozen head: the full 40-hex SHA given to pr freeze (required; never guessed)")
		}
		if verb == "seat" {
			c.Flags().StringVar(&slot, "slot", "", "the scheduled slot this seat fills, as given to pr schedule (required)")
			c.Flags().StringVar(&supersedes, "supersedes", "", "when replacing a reviewer: the current seat task of this slot (refused if that seat already gave a negative verdict at this head)")
		}
		if verb == "verdict" {
			c.Flags().StringVar(&verdict, "verdict", "", "required: APPROVE, CHANGES_REQUESTED, PASS, FAIL, CARRY or REFUSED (meanings in the description above)")
			c.Flags().StringVar(&node, "node", "", "optional: the Hadron node holding your saved review (recorded as text, not fetched)")
			c.Flags().StringVar(&receiptURL, "receipt", "", "optional: the URL of your signed PR comment (recorded as text, not fetched)")
		}
		if verb == "withdraw" {
			c.Flags().StringVar(&receiptURL, "receipt", "", "optional: the URL of a signed PR comment saying why (recorded as text, not fetched)")
			addTaskInstructionFlag(c, &rawInstruction)
		}
		parent.AddCommand(c)
	}
	return parent
}

// prRegisterLine renders the server's optional register guidance (the
// receipt's action answer, cor:agt:130:04) as ONE self-describing line, so the
// write's success is what the receipt leads with and a state is never read as
// "the action was not recorded" (cli#2941). UNMAPPED is the honest answer for
// the pr-protocol codes while no entry ships for them. WITHHELD means NO
// lookup was made, so it never says "no register entry matches" — that would
// be false and could disclose what the caller may not read. A state this CLI
// does not know is printed as given, never mapped to one it does. No branch
// fabricates a loc or URN. The --json shape is unchanged.
func prRegisterLine(a cmdutil.ActionAnswerDTO) string {
	code := output.SafeCell(a.Code)
	switch a.State {
	case "MAPPED":
		if a.TaskURN != nil && *a.TaskURN != "" {
			return "register " + code + " → " + output.SafeCell(*a.TaskURN)
		}
		return "register " + code + " → mapped, but the server named no task"
	case "UNRESOLVED":
		if a.TaskLoc != nil && *a.TaskLoc != "" {
			return "register " + code + " → no runnable task found at " + output.SafeCell(*a.TaskLoc)
		}
		return "register " + code + " → no runnable task found"
	case "UNMAPPED":
		return "register " + code + " → no register entry matches this code"
	case "WITHHELD":
		return "register " + code + " → not shown to this caller"
	default:
		return "register " + code + " → state " + output.SafeCell(a.State)
	}
}

func prCell(value *string) string {
	if value == nil {
		return "—"
	}
	return output.SafeCell(*value)
}

// Protocol conflicts carry typed slots; ordinary task refusals retain the
// task mapper's terminal-safe code prefix and their JSON serverCode.
func mapPRError(err error) error {
	mapped := api.MapPRProtocolError(err)
	var protocol interface{ PRReadinessSlots() []api.PRSlotFailure }
	if errors.As(mapped, &protocol) {
		return mapped
	}
	return api.WithServerCode(mapTaskError(err), err)
}

const prGroupLong = `Review a pull request through a recorded protocol: the team says which reviews
a PR needs, fixes the commit under review, binds a reviewer to each review,
collects the verdicts and checks that every one is in. It records facts; it
does not read GitHub, check CI, or merge the PR.

The flow, in order. <build-task> is the PR's own task (it must carry a
pull_request reference), a seat is a child task of it that does one review,
a slot is one required review (such as local, blind or qa), and the head is
the PR's tip commit:
  1. pr schedule    the owner lists the slots the PR needs
  2. pr freeze      the build task's assignee fixes the head under review
  3. pr seat        the owner binds a seat task to each slot at that head
  4. pr verdict     each reviewer records a verdict on their seat (pr withdraw
                    clears a finished negative one: its reviewer or a coordinator)
  5. pr readiness   anyone reads whether every slot counts at the head
  6. pr ready       the build task's assignee records the ready call
After a push, freeze the new head and bind new seats (pr seat --supersedes
names the seat it replaces): a verdict only counts at the head its seat was
bound to. Each command's own page says who may run it and what it refuses.

Owner chain means ` + taskAssignerDoc + `. The assignee is the person or
worker assigned to that task. The server judges authority and readiness; these
commands do not check model families, merge a PR or finish a seat task. A task
may be given as an id, handle or URN (as in team task get). A bare handle is
qualified with --app when you give it; pr readiness, a read, also uses the App
of this worktree's worker-session binding, and the other pr commands resolve a
bare handle through the worker session they send. Otherwise a bare handle
may need its App named when several are visible (TASK_HANDLE_QUALIFY).

Each write appends an event to the build task (review.scheduled, pr.frozen,
review.seat.assigned, review.verdict, pr.ready): read them with hadron team
events --task <build-task> --type <event>. A bound worker session writes as that
worker, otherwise you write as the signed-in person; an App key may read
readiness but not write.

Each command makes one request and never retries or guesses a head. A write
prints the task handle, id and status, then one register line (the register is
the team's table that maps an action code to the task that handles it): the
register's answer about the action code, as "register <code> → <task
URN>", "→ no runnable task found at <loc>", "→ no register entry matches
this code" or "→ not shown to this caller". The state UNMAPPED is the "no
register entry" answer, and every pr code gets it while no register entry
ships for it. The line says where the code routes, never whether the write
recorded: the task line is the receipt. Text prints no placeholder for a
missing loc or URN. --json is
{task:{id,handle,status},action:{code,state,taskLoc,taskUrn}} (taskLoc and
taskUrn are null when the state carries none). Readiness prints READY or NOT
READY and a row per slot; --json is {ready,head,slots:[{slot,currentTaskId,state,verdict,
head,counts}]}. NOT READY is a successful read (exit 0). A refusal such as
PR_NOT_READY (which names the slots that do not count) or a stale head exits 5,
with serverCode in JSON; exit 7 means the outcome is unknown, so read the
task's events (hadron team events) before repeating a write. Evidence links
are recorded as text and never fetched: put no secrets in them. A server
without this protocol refuses the commands.`

const prGroupExample = `  hadron team pr schedule AT0.7850 --slot local --slot blind --slot qa
  hadron team pr freeze AT0.7850 0123456789abcdef0123456789abcdef01234567
  hadron team pr seat AT0.7850.1 --slot local --head 0123456789abcdef0123456789abcdef01234567
  hadron team pr verdict AT0.7850.1 --head 0123456789abcdef0123456789abcdef01234567 --verdict APPROVE
  hadron team pr readiness AT0.7850 --head 0123456789abcdef0123456789abcdef01234567
  hadron team pr ready AT0.7850 --head 0123456789abcdef0123456789abcdef01234567`

// prReviewRules is shared by "pr seat" and "pr verdict" (cli#2785): the order, what a
// negative verdict does, and the remedy when a seat cannot bind or record. Only a
// verdict recorded with "pr verdict" counts toward readiness; a signed PR receipt is
// evidence beside it.
const prReviewRules = `

The order is schedule, freeze, seat, verdict, finish. A seat binds to a task
that has not ended (PENDING or STARTED); only pr verdict needs it STARTED, and a
finished seat cannot take a verdict. A negative verdict at a head blocks every
slot there until the head moves, its own reviewer re-rates it, or (once the
seat is finished) its reviewer or a coordinator withdraws it with pr withdraw.

When a seat cannot bind or record (TASK_ENDED, REVIEW_SEAT_UNASSIGNED or
REVIEW_HEAD_MISMATCH), only a verdict recorded with pr verdict counts: create a
fresh seat task and bind it with --supersedes at the same head, and its reviewer
records the verdict and finishes. A signed PR receipt at the exact head is
evidence, not the record: pass it with --receipt.
`

const prSeeGroup = "\nThe flow and the exit codes are on \"hadron team pr --help\"."

var prLeafLong = map[string]string{
	"schedule": `Say which reviews a PR needs: one --slot per required review, such as local,
blind or qa. Run it on the PR's build task, normally first (it can be changed
later with the same command). Only the task's owner chain may run it: the
assignee alone may not (the group page "hadron team pr --help" defines the
owner chain).

Each call REPLACES the whole list: repeat every slot you still want. The slot
ids are the ones "pr seat --slot" and "pr readiness" use, so keep them
identical. A slot may be dropped, but not one whose seat already gave a
negative verdict at the frozen head (REVIEW_SEAT_NOT_SUPERSEDABLE), unless that
negative was withdrawn (pr withdraw). Prints the build task and the register
line.

To see which slots are scheduled before a head is frozen, run hadron team task
get <build-task> (its properties list review.schedule), or hadron team events
--task <build-task> --type review.scheduled for each change.` + prSeeGroup,

	"freeze": `Record the exact commit under review: the PR's current head, the full 40-hex
SHA (git rev-parse HEAD, or the PR page). Reviews and readiness refer to this
head, so after a push freeze the new one. Only the build task's assignee may.

The build task must carry a pull_request reference (hadron team task update
<task> --ref pull-request=<PR URL>; --ref replaces the whole list); without it
the server refuses with PR_REFERENCE_MISSING. A head that already has a negative
seat verdict cannot be frozen again, unless that verdict was withdrawn (pr
withdraw): otherwise answer the finding with a new head. Normally run it
after "pr schedule" and before "pr seat". Prints the build task and the
register line.` + prSeeGroup,

	"seat": `Bind a seat task to a scheduled slot at the frozen head: this is how a reviewer
is named for that review. Only the seat task's owner chain may: its owner (the
build task's assignee, who created it), a worker the owner reports to, or
whoever manages the App. The seat's own assignee alone may not (TASK_NOT_OWNER).

The seat task must already exist, as a direct child of the build task (hadron
team task create "..." --parent <build-task> --assignee worker:<name>). The
slot must be one given to "pr schedule" (REVIEW_SLOT_UNKNOWN otherwise) and
--head the frozen head (the full SHA). A seat task is bound once
(REVIEW_SEAT_ALREADY_ASSIGNED).

To replace a reviewer or move to a new head, create a new seat task and pass
the slot's current seat task as --supersedes (REVIEW_SEAT_SUPERSEDES_REQUIRED
if you do not). A seat that already gave CHANGES_REQUESTED, FAIL or REFUSED at
this head cannot be replaced or moved (REVIEW_SEAT_NOT_SUPERSEDABLE): answer the
finding with a new head. Prints the seat task and the register line.` + prReviewRules + prSeeGroup,

	"verdict": `Record your verdict on a seat you were assigned, without finishing the seat
task. Only the seat's assignee may, the task must be STARTED, the seat must
have been bound with "pr seat" (REVIEW_SEAT_UNASSIGNED otherwise) and --head
must be the head it was bound to (REVIEW_HEAD_MISMATCH). Record it, then finish
the seat task (hadron team task finish <seat-task> --outcome <one word, such
as approved>, adding --findings-file <path> to attach findings): a seat counts
toward readiness only once finished.

The verdicts: APPROVE and CHANGES_REQUESTED are for a code review; PASS and
FAIL for QA or another check (the team's convention: the server accepts any of
the six on any seat). CARRY says your earlier verdict still holds at this new
head, because what you reviewed did not change. REFUSED records that the
reviewer refused the review; the protocol defines it only as a negative
verdict, so it blocks readiness like CHANGES_REQUESTED. APPROVE, PASS and CARRY
count toward readiness; CHANGES_REQUESTED, FAIL and REFUSED are
negative and block it at this head (only the reviewer who recorded a negative
verdict may re-rate it at that head).

--node names the Hadron node holding your saved review and --receipt the URL of
your signed PR comment; both are recorded as text, not fetched. Prints the seat
task and the register line.` + prReviewRules + prSeeGroup,

	"withdraw": `Withdraw a finished seat's negative verdict at the head it was recorded for
(CHANGES_REQUESTED, FAIL or REFUSED), so it stops blocking. Use it when the
finding was answered or the verdict was recorded in error and you cannot
re-rate it: a seat that is still STARTED is re-rated by its own reviewer with
pr verdict instead (TASK_INVALID_TRANSITION otherwise). Only the reviewer who
recorded the verdict may, or a coordinator: a worker whose current role is
coordinator in the App, or whoever manages the App.

--head must be the head the verdict was recorded at (REVIEW_HEAD_MISMATCH
otherwise); --receipt is the URL of a signed PR comment saying why, recorded as
text and never fetched. The seat must have been bound and its latest verdict at
that head must be negative and not already withdrawn
(REVIEW_VERDICT_NOT_NEGATIVE, REVIEW_VERDICT_ALREADY_WITHDRAWN). Anyone else is
refused (REVIEW_WITHDRAW_FORBIDDEN): a worker session that manages the App only
through the person holding it is refused with no instruction; pass --instruction
(below), or ask the reviewer or a coordinator.

` + taskInstructionHelpShort(taskInstructionFloor) + `
The claim rides this one write and the server decides; the CLI infers nothing
about the worker's authority and never resends the withdraw without the flag.

A withdrawal is never an approval. The verdict stays in the record and the seat
is not changed; the negative only stops blocking these: readiness, freezing
this head again, dropping the slot, and moving or replacing the seat. The slot
still needs a superseding seat (pr seat --supersedes) whose own APPROVE, PASS
or CARRY counts at this head. A refused call writes nothing, so a refusal
needs no repeat. Prints the seat task and the register line.` + prSeeGroup,

	"readiness": `Show whether every scheduled review is in at a head: READY, or NOT READY with
a row per slot saying which seat, state and verdict it has and whether it
counts. Anyone who can read the task may run it; it changes nothing.

--head is the frozen head (the full SHA you gave to "pr freeze"); it is never
guessed. A slot counts when its current seat is finished, was bound at this
head, holds an APPROVE, PASS or CARRY verdict from someone other than the build
task's assignee or the head's freezer (the person who ran pr freeze on this
head), and no seat of the head holds a negative verdict (one that was
withdrawn with pr withdraw no longer counts as negative). NOT READY is a
successful read (exit 0): fix the slots that do not count (seat a reviewer, wait
for the verdict, finish the seat task), then run it again. When every slot
counts, the build task's assignee runs "pr ready".

To see which slots are scheduled before a head is frozen, run hadron team task
get <build-task> (its properties list review.schedule), or hadron team events
--task <build-task> --type review.scheduled for each change.` + prSeeGroup,

	"ready": `Record the ready call: the build task's assignee says every scheduled review
counts at the frozen head. It records that fact and nothing else: it does not
merge the PR or check CI. Only the build task's assignee may.

Run "pr readiness" first. If any slot does not count the server refuses with
PR_NOT_READY (exit 5) and names the failing slots; a head that is not the frozen
one is refused too. A refused call records nothing. Prints the build task and
the register line.` + prSeeGroup,
}

var prLeafExample = map[string]string{
	"schedule":  "  hadron team pr schedule AT0.7850 --slot local --slot blind --slot qa",
	"freeze":    "  hadron team pr freeze AT0.7850 0123456789abcdef0123456789abcdef01234567",
	"seat":      "  hadron team pr seat AT0.7850.1 --slot local --head 0123456789abcdef0123456789abcdef01234567\n  hadron team pr seat AT0.7850.4 --slot local --head <new-head> --supersedes AT0.7850.1",
	"verdict":   "  hadron team pr verdict AT0.7850.1 --head 0123456789abcdef0123456789abcdef01234567 --verdict APPROVE --receipt https://github.com/acme/app/pull/9#issuecomment-1",
	"withdraw":  "  hadron team pr withdraw AT0.7850.1 --head 0123456789abcdef0123456789abcdef01234567 --receipt https://github.com/acme/app/pull/9#issuecomment-2",
	"readiness": "  hadron team pr readiness AT0.7850 --head 0123456789abcdef0123456789abcdef01234567 --json",
	"ready":     "  hadron team pr ready AT0.7850 --head 0123456789abcdef0123456789abcdef01234567",
}
