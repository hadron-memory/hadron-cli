package team

// `team availability` — worker and model-family availability (#830 over
// server #1706; cor:agt:060:05 free-for-work, :08 who may set a worker's,
// :09 who may set a family's). `get` renders the App's records; `set` writes
// one record (an authored write when a session is bound).

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

var availabilityStates = map[string]gen.AvailabilityState{
	"available":   gen.AvailabilityStateAvailable,
	"unavailable": gen.AvailabilityStateUnavailable,
}

var availabilityReasons = map[string]gen.AvailabilityReason{
	"HOST_DOWN":     gen.AvailabilityReasonHostDown,
	"OTHER":         gen.AvailabilityReasonOther,
	"OUT_OF_TOKENS": gen.AvailabilityReasonOutOfTokens,
	"STALLED":       gen.AvailabilityReasonStalled,
}

func newCmdTeamAvailability(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "availability <command>",
		Short: "Worker and model-family availability for work",
		Long: `Who and what is free for work in the team App (cor:agt:060:05).
` + "`get`" + ` shows every worker's and family's record; ` + "`set`" + ` writes one.

Setting a WORKER's availability is the worker itself (only ever to
unavailable), the person holding its name, or whoever manages the App
(cor:agt:060:08). Setting a FAMILY's is whoever manages the App
(cor:agt:060:09). A reason is required when unavailable and refused when
available; --until (RFC 3339, future) is an unavailable-only bound — a
passed until reads as available at once. Then, for a WORKER record, within
a minute the server records the lapse: the record becomes available, set by
LAPSE (setBy in --json), which ` + "`get`" + ` shows as "AVAILABLE (lapsed
at <time>)" and ` + "`team events`" + ` as an availability.changed by "lapse at
<time>"; a family record is never rewritten, and its passed until only
reads as available.`,
	}
	cmd.AddCommand(newCmdTeamAvailabilityGet(f))
	cmd.AddCommand(newCmdTeamAvailabilitySet(f))
	return cmd
}

// ---- get ------------------------------------------------------------------

func newCmdTeamAvailabilityGet(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Show every worker's and family's availability",
		Long:  `Render the App's availability records — every worker and every model family, with state, reason, until and whether each is in effect now. A worker record whose until passed reads "AVAILABLE (lapsed at <time>)" once the server records the lapse (setBy LAPSE in --json).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			appLabel := lazyAppLabel(ctx, f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamAvailability(ctx, client, scope.Ref)
			if err != nil {
				return api.MapError(err)
			}
			dto := teamAvailabilityDTO{Workers: []workerAvailabilityDTO{}, Families: []familyAvailabilityDTO{}}
			for _, w := range resp.TeamAvailability.Workers {
				if w == nil {
					continue
				}
				party := taskPartyDTO{}
				if p := partyDTO(w.Worker); p != nil {
					party = *p
				}
				dto.Workers = append(dto.Workers, workerAvailabilityDTO{
					Worker:     party,
					State:      string(w.State),
					Reason:     reasonPtr(w.Reason),
					Until:      w.Until,
					SetAt:      w.SetAt,
					SetBy:      string(w.SetBy),
					SetByParty: partyDTO(w.SetByParty),
					InEffect:   w.InEffect,
				})
			}
			for _, fam := range resp.TeamAvailability.Families {
				if fam == nil {
					continue
				}
				dto.Families = append(dto.Families, familyAvailabilityDTO{
					AppID:    fam.AppId,
					Family:   fam.Family,
					State:    string(fam.State),
					Reason:   reasonPtr(fam.Reason),
					Until:    fam.Until,
					SetAt:    fam.SetAt,
					SetBy:    partyDTO(fam.SetBy),
					InEffect: fam.InEffect,
				})
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				// Which App's records, from where (#470) — before both tables,
				// so an all-available team (no records) still names its scope.
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				return renderAvailabilityTable(w, dto)
			})
		},
	}
	return cmd
}

// renderAvailabilityTable renders both halves. Flush errors propagate
// (cycle 1, Diego P3-2): a broken pipe mid-table must not report success.
func renderAvailabilityTable(w io.Writer, dto teamAvailabilityDTO) error {
	workers := output.NewTable(w, "WORKER", "STATE", "REASON", "UNTIL", "IN EFFECT")
	for _, e := range dto.Workers {
		workers.Row(partyCell(&e.Worker), workerStateCell(e), strOr(e.Reason), strOr(e.Until), yesNo(e.InEffect))
	}
	if err := workers.Flush(); err != nil {
		return err
	}
	families := output.NewTable(w, "FAMILY", "STATE", "REASON", "UNTIL", "IN EFFECT")
	for _, e := range dto.Families {
		families.Row(e.Family, e.State, strOr(e.Reason), strOr(e.Until), yesNo(e.InEffect))
	}
	return families.Flush()
}

// workerStateCell renders a worker record's state. A record the platform
// rewrote when its until passed (#858 over server #1851, cor:agt:060:02) is
// AVAILABLE set by LAPSE, its until cleared and its setAt the instant it
// lapsed, so the lapse is said here or the row would read as an unexplained
// change. Any other setter, and any server without LAPSE, renders as before.
func workerStateCell(e workerAvailabilityDTO) string {
	if e.SetBy == string(gen.AvailabilitySetterLapse) {
		return fmt.Sprintf("%s (lapsed at %s)", e.State, e.SetAt)
	}
	return e.State
}

// workerRefIsSelfSufficient answers whether a --worker value names its
// worker with no App scope: a worker URN (hrn:worker:…, the #991 shape
// resolveWorker dispatches on first) or the row id's 32-hex form. A bare
// NAME is App-scoped (cor:agt:020:02) and needs the App.
func workerRefIsSelfSufficient(ref string) bool {
	if strings.HasPrefix(strings.ToLower(ref), "hrn:worker:") {
		return true
	}
	if len(ref) != 32 {
		return false
	}
	for _, r := range ref {
		if !strings.ContainsRune("0123456789abcdef", unicode.ToLower(r)) {
			return false
		}
	}
	return true
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// ---- set ------------------------------------------------------------------

func newCmdTeamAvailabilitySet(f *cmdutil.Factory) *cobra.Command {
	var workerRef, family, state, reason, until string
	cmd := &cobra.Command{
		Use:   "set (--worker <ref> | --family <token>) --state <available|unavailable>",
		Short: "Set a worker's or a family's availability",
		Long: `Set one availability record. --worker takes the worker's id, URN or
name: an id or URN names its worker on its own, while a NAME needs the App
(--app, a context, or a worktree binding). --family takes the family token
(e.g. claude, codex) and always needs the App. --state is available or
unavailable; --reason (HOST_DOWN, OTHER, OUT_OF_TOKENS, STALLED) is required
with unavailable and refused with available; --until (RFC 3339, in the
future) bounds an unavailable record.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if workerRef == "" && family == "" {
				return exitcode.Newf(exitcode.Usage, "name what to set: --worker <id, URN or name> or --family <token>")
			}
			if workerRef != "" && family != "" {
				return exitcode.Newf(exitcode.Usage, "--worker and --family: set one record at a time")
			}
			st, ok := availabilityStates[strings.ToLower(state)]
			if !ok {
				return exitcode.Newf(exitcode.Usage, "--state takes available or unavailable, got %q", state)
			}
			var reasonIn *gen.AvailabilityReason
			if reason != "" {
				r, ok := availabilityReasons[strings.ToUpper(reason)]
				if !ok {
					return exitcode.Newf(exitcode.Usage, "--reason takes HOST_DOWN, OTHER, OUT_OF_TOKENS or STALLED, got %q", reason)
				}
				reasonIn = &r
			}
			if st == gen.AvailabilityStateAvailable {
				if reasonIn != nil {
					return exitcode.Newf(exitcode.Usage, "--reason belongs to unavailable records; an available record carries none")
				}
				if until != "" {
					return exitcode.Newf(exitcode.Usage, "--until bounds an unavailable record; an available record carries none")
				}
			} else if reasonIn == nil {
				return exitcode.Newf(exitcode.Usage, "an unavailable record needs --reason (HOST_DOWN, OTHER, OUT_OF_TOKENS or STALLED)")
			}
			if until != "" {
				if _, err := time.Parse(time.RFC3339, until); err != nil {
					return exitcode.Newf(exitcode.Usage, "--until takes an RFC 3339 timestamp, e.g. 2026-10-13T00:00:00Z, got %q", until)
				}
			}
			// No worktree and no App context is a LEGITIMATE caller here: a
			// worker id or URN names itself (below), so readBinding's
			// "not inside a git worktree" is not the actionable message the
			// way readBindingOrNilWithApp treats it for the chat commands.
			b, _, err := readBinding(ctx)
			if err != nil && !errors.Is(err, errNoWorktree) {
				return err
			}
			if err != nil {
				b = nil
			}
			// The App: a family record always names one; a worker record
			// sends it when it resolves (a NAME needs it — names are
			// App-scoped) and omits it for the self-sufficient spellings,
			// exactly the split the server's input documents (cycle 1,
			// Diego P3-1: the URN path demanded scope the help never
			// promised).
			var appRef *string
			if scope, err := resolveTeamAppScope(ctx, f, b); err == nil {
				appRef = &scope.Ref
			} else if family != "" {
				return err
			} else if !workerRefIsSelfSufficient(workerRef) {
				return exitcode.Newf(exitcode.Usage,
					"a worker NAME needs the App — pass --app, set an App context, or bind a worker session (a worker id or URN names itself), got %q", workerRef)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if workerRef != "" {
				input := gen.SetWorkerAvailabilityInput{
					AppRef: appRef,
					Worker: workerRef,
					State:  st,
					Reason: reasonIn,
					Until:  optStr(until),
				}
				resp, err := gen.WorkerAvailabilitySet(ctx, client, &input)
				if err != nil {
					return api.MapError(err)
				}
				rec := workerAvailabilityDTOFrom(*resp.SetWorkerAvailability)
				return output.Write(f.IOStreams, f.JSON, rec, func(w io.Writer) error {
					return writeAvailabilityReceipt(w, partyCell(&rec.Worker), rec.State, rec.Reason, rec.Until)
				})
			}
			input := gen.SetModelFamilyAvailabilityInput{
				AppRef: *appRef,
				Family: family,
				State:  st,
				Reason: reasonIn,
				Until:  optStr(until),
			}
			resp, err := gen.ModelFamilyAvailabilitySet(ctx, client, &input)
			if err != nil {
				return api.MapError(err)
			}
			r := resp.SetModelFamilyAvailability
			rec := familyAvailabilityDTO{
				AppID:    r.AppId,
				Family:   r.Family,
				State:    string(r.State),
				Reason:   reasonPtr(r.Reason),
				Until:    r.Until,
				SetAt:    r.SetAt,
				SetBy:    partyDTO(r.SetBy),
				InEffect: r.InEffect,
			}
			return output.Write(f.IOStreams, f.JSON, rec, func(w io.Writer) error {
				return writeAvailabilityReceipt(w, rec.Family, rec.State, rec.Reason, rec.Until)
			})
		},
	}
	cmd.Flags().StringVar(&workerRef, "worker", "", "the worker: its id, URN or name (a name needs the App)")
	cmd.Flags().StringVar(&family, "family", "", "the model family token, e.g. claude or codex")
	cmd.Flags().StringVar(&state, "state", "", "available or unavailable (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "HOST_DOWN, OTHER, OUT_OF_TOKENS or STALLED (required with unavailable)")
	cmd.Flags().StringVar(&until, "until", "", "RFC 3339 time the unavailable record lapses at")
	return cmd
}

func workerAvailabilityDTOFrom(r gen.WorkerAvailabilitySetSetWorkerAvailability) workerAvailabilityDTO {
	party := taskPartyDTO{}
	if p := partyDTO(r.Worker); p != nil {
		party = *p
	}
	return workerAvailabilityDTO{
		Worker:     party,
		State:      string(r.State),
		Reason:     reasonPtr(r.Reason),
		Until:      r.Until,
		SetAt:      r.SetAt,
		SetBy:      string(r.SetBy),
		SetByParty: partyDTO(r.SetByParty),
		InEffect:   r.InEffect,
	}
}

func writeAvailabilityReceipt(w io.Writer, who, state string, reason, until *string) error {
	if _, err := fmt.Fprintf(w, "✓ %s is %s", who, strings.ToLower(state)); err != nil {
		return err
	}
	if reason != nil {
		if _, err := fmt.Fprintf(w, " (%s)", *reason); err != nil {
			return err
		}
	}
	if until != nil {
		if _, err := fmt.Fprintf(w, " until %s", *until); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}
