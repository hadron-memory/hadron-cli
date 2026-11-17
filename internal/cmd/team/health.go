package team

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/contextsize"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// healthStatusReader is a command/JSON boundary, never a classifier import.
// It returns the status command's exit separately and discards its diagnostics.
type healthStatusReader func(context.Context, string, *hostpoller.Config) ([]byte, int, error)

type healthReport struct {
	Worker     string  `json:"worker"`
	Status     string  `json:"status"`
	Prompt     string  `json:"prompt"`
	Reason     *string `json:"reason"`
	LastTurnAt *string `json:"lastTurnAt"`
	CheckedAt  string  `json:"checkedAt"`
	// ContextTokens and ContextMeasuredAt travel together or not at all
	// (hadron-cli#2063): absent means "not measured", never 0.
	ContextTokens     *int    `json:"contextTokens,omitempty"`
	ContextMeasuredAt *string `json:"contextMeasuredAt,omitempty"`
}
type healthRefusal struct {
	Worker string `json:"worker"`
	Code   string `json:"code"`
}
type healthReceipt struct {
	Reports []healthReport  `json:"reports"`
	Refused []healthRefusal `json:"refused"`
	// Renames is the title pass (cli#2017): one entry per tab renamed, or that
	// a dry run would rename. Always a list.
	Renames []healthRename `json:"renames"`
	// OrcaVersion is what `orca --version` printed when the title pass ran
	// (cli#2166); null when it did not run or Orca gave no answer. Local
	// receipt only, never sent to the platform.
	OrcaVersion *string `json:"orcaVersion"`
	// DryRun is the shared #2017/#2027 posture: true unless the run passed
	// --dry-run=false (the scheduled entry always does).
	DryRun bool        `json:"dryRun"`
	Fixes  []dupeFix   `json:"fixes"`
	Alerts []dupeAlert `json:"alerts"`
	Skips  []dupeSkip  `json:"skips"`
}

// Decode only the versioned wire contract. Detail, terminal text and unknown
// fields are never retained in reports, error messages or the API payload.
func decodeHealthStatus(raw []byte) ([]healthReport, error) {
	var wire []struct {
		Contract   string  `json:"contract"`
		Worker     string  `json:"worker"`
		Status     string  `json:"status"`
		Prompt     string  `json:"prompt"`
		Reason     *string `json:"reason"`
		LastTurnAt *string `json:"lastTurnAt"`
		CheckedAt  string  `json:"checkedAt"`
	}
	bad := func() ([]healthReport, error) {
		return nil, exitcode.Newf(exitcode.Error, "invalid terminal-status@1 report; nothing written")
	}
	if len(raw) > 8<<20 {
		return bad()
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if dec.Decode(&wire) != nil || wire == nil {
		return bad()
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return bad()
	}
	out := []healthReport{}
	seen := map[string]bool{}
	for _, r := range wire {
		if r.Contract != "terminal-status@1" {
			return nil, exitcode.Newf(exitcode.Error, "unsupported terminal status contract; expected terminal-status@1; nothing written")
		}
		if strings.TrimSpace(r.Worker) == "" || seen[r.Worker] {
			return bad()
		}
		seen[r.Worker] = true
		switch r.Status {
		case "busy", "idle", "locked", "dark":
		default:
			return bad()
		}
		switch r.Prompt {
		case "empty", "not_empty", "menu", "unknown":
		default:
			return bad()
		}
		if r.Reason != nil {
			switch *r.Reason {
			case "bare_shell", "guardrail_triggered", "menu_or_question", "model_at_capacity", "no_agent", "no_terminal", "no_turn_for_due_ticker", "rate_limited", "shed_staged_not_relaunched", "token_budget_exhausted", "unrecognized":
			default:
				return bad()
			}
		}
		if (r.Status == "busy" || r.Status == "idle") != (r.Reason == nil) {
			return bad()
		}
		checked, e := time.Parse(time.RFC3339Nano, r.CheckedAt)
		if e != nil {
			return bad()
		}
		if r.LastTurnAt != nil {
			last, e := time.Parse(time.RFC3339Nano, *r.LastTurnAt)
			if e != nil || last.After(checked) {
				return bad()
			}
		}
		out = append(out, healthReport{Worker: r.Worker, Status: r.Status, Prompt: r.Prompt, Reason: r.Reason, LastTurnAt: r.LastTurnAt, CheckedAt: r.CheckedAt})
	}
	return out, nil
}

func newCmdTeamHealth(f *cmdutil.Factory) *cobra.Command {
	return newCmdTeamHealthWithReader(f, readHealthStatusCommand)
}
func newCmdTeamHealthWithReader(f *cmdutil.Factory, read healthStatusReader) *cobra.Command {
	group := &cobra.Command{Use: "health", Short: "Report this host's workers' terminal health"}
	var configPath string
	var statusTimeout time.Duration
	var dryRun bool
	c := &cobra.Command{Use: "sweep --config <host.yaml>", Short: "Read terminal status and store code-only reports for this host", Args: cobra.NoArgs,
		Long: `Read terminal-status@1 JSON from "hadron orca terminal status --all --json"
and store the status of this host's registered workers in one batch. The status
command alone classifies terminals; this binary runs its own status command.
Screen text is read only to classify, never kept: it reaches no report, log or
API payload, and the sweep never types into any terminal.

After the reports are stored, the sweep also detects duplicates (#2027): more
than one agent process in a bound worker's worktree, and more than one terminal
tab for it. A manual run is DRY (--dry-run, the default): it lists what it
found and changes nothing. --dry-run=false stops the unsupervised duplicates —
a SIGTERM to exactly those processes, never to a supervised agent, never to a
lone agent, never to a worktree whose worker has no session binding — and
closes extra bare-shell tabs, one notification line posted to the team chat
from the sweep's own bound session. A team pause in force, or a worker whose
context reset has not ended, means detect and report only.

Use the same --config host YAML and 0600 key_file as "team host run". The host
key is never supplied as an argument or environment variable. Orca is resolved
from delivery.orca_bin when set, otherwise PATH. Paused workers are still reported.
No alarm is computed, and an App without a coordinator is valid. An unresolved,
ambiguous or missing registered worker report refuses the entire batch before
writing (exit 1). A missing or non-live host registration is exit 8.
Status rows for workers not registered on this host are ignored.

Exit 0 after a successful write, including per-worker refusals (printed, not
retried) and duplicate findings that could not be acted on (they become alerts);
1 for a failed write or malformed status contract, 3 for a missing or
rejected key, 7 when the server is unreachable, 8 for forbidden and 10 when the
status command cannot reach Orca. The status read is allowed 5 seconds per
registered worker (at least 2 minutes); --status-timeout overrides that, and a
read that runs out of time says "terminal status timed out after <duration>"
(exit 1, nothing written). No scheduling or recovery is installed.
Tab titles. After the batch is stored, the sweep sets the title of every Orca
tab in a registered worker's worktree to the worker's bare name
("hadron team worker terminal-title"), when it differs; a second sweep changes
nothing. A tab is matched by its worktree's session binding, so a plain shell
or a worktree with no registered worker is never touched. --dry-run is on by
default: a manual run lists the renames it would make and changes nothing; the
installed timer passes --dry-run=false. A tab that fails to rename is listed
with its error, the rest still go ahead, and the sweep then exits 1. The title
pass needs Orca 1.4.232 or later (terminal list and rename with a tab title);
the receipt carries the version "orca --version" printed (orcaVersion, null when
Orca gave no version), for the operator only: it is never sent to the platform.
The pass runs delivery.orca_bin when that path resolves, else "orca" on PATH.

Duplicate agents and tabs. Also after the batch is stored, the sweep detects
more than one agent process and more than one terminal tab per bound-worker
worktree: the same --dry-run posture lists the duplicate agents it would stop
(the unsupervised ones, a pidfd-pinned SIGTERM) and the extra bare-shell tabs
it would close, and --dry-run=false applies them; a team pause or an unfinished
context reset means report only. Duplicate findings that could not be acted on
become alerts and never fail the sweep.
--json contains the sent reports (including refused items), per-worker
refusals, dryRun, renames, and the fixes, alerts and skips of the duplicate
phase; never screen text. A report in this list is not a storage receipt.`,
		RunE: func(c *cobra.Command, _ []string) error {
			if statusTimeout < 0 {
				return exitcode.Newf(exitcode.Usage, "--status-timeout must not be negative; nothing written")
			}
			cfg, _, server, err := loadHostPoller(f, configPath)
			if err != nil {
				if exitcode.FromError(err) == exitcode.Usage {
					return exitcode.Newf(exitcode.Usage, "invalid host configuration; nothing written")
				}
				return healthSafeError(err)
			}
			state, err := hostpoller.LoadState(cfg.StateFile)
			if err != nil || state.RegisteredHostID() == "" {
				return exitcode.Newf(exitcode.Error, "cannot read registered host state; nothing written")
			}
			regs, err := gen.HostRegistrations(c.Context(), server.client, cfg.App)
			if err != nil {
				return healthSafeError(err)
			}
			names := map[string]string{}
			dupesWorkers := []dupeWorker{}
			live := false
			if regs != nil {
				for _, reg := range regs.HostRegistrations {
					if reg != nil && reg.HostId == state.RegisteredHostID() {
						live = reg.Live
						ids := map[string]bool{}
						for _, w := range reg.Workers {
							if w == nil || w.WorkerId == "" || w.Worker == nil || w.Worker.Id != w.WorkerId || strings.TrimSpace(w.Worker.Name) == "" || ids[w.WorkerId] {
								return exitcode.Newf(exitcode.Error, "cannot resolve registered worker; nothing written")
							}
							if _, duplicate := names[w.Worker.Name]; duplicate {
								return exitcode.Newf(exitcode.Error, "ambiguous registered worker name; nothing written")
							}
							ids[w.WorkerId] = true
							names[w.Worker.Name] = w.WorkerId
							urn := ""
							if w.Worker.Urn != nil {
								urn = *w.Worker.Urn
							}
							dupesWorkers = append(dupesWorkers, dupeWorker{workerID: w.WorkerId, workerName: w.Worker.Name, urn: urn})
						}
					}
				}
			}
			if !live {
				return exitcode.Newf(exitcode.Forbidden, "host registration is not live; nothing written")
			}
			limit := statusTimeout
			if limit <= 0 {
				limit = healthStatusDeadline(len(names))
			}
			raw, code, err := read(withHealthStatusTimeout(c.Context(), limit), configPath, cfg)
			if code == 10 {
				return exitcode.Newf(exitcode.OrcaUnreachable, "Orca is not reachable on this host; nothing written")
			}
			if err != nil || code != 0 {
				var timedOut errHealthStatusTimeout
				if errors.As(err, &timedOut) {
					return exitcode.Newf(exitcode.Error, "%s; nothing written", timedOut.Error())
				}
				if code == 3 || code == 7 || code == 8 {
					return exitcode.Newf(code, "terminal status command failed; nothing written")
				}
				return exitcode.Newf(exitcode.Error, "terminal status command failed; nothing written")
			}
			rows, err := decodeHealthStatus(raw)
			if err != nil {
				return err
			}
			dto := healthReceipt{Reports: []healthReport{}, Refused: []healthRefusal{}, Renames: []healthRename{}, DryRun: dryRun, Fixes: []dupeFix{}, Alerts: []dupeAlert{}, Skips: []dupeSkip{}}
			inputs := []*gen.WorkerTerminalReportInput{}
			matched := map[string]bool{}
			display := map[string]string{}
			contextMeasurer := &contextsize.Measurer{}
			for _, r := range rows {
				if id, ok := names[r.Worker]; ok {
					display[id] = r.Worker
					matched[id] = true
					r.Worker = id
					if m := healthContext(cfg, state, id, display[id], contextMeasurer); m != nil {
						tokens, at := m.Tokens, m.MeasuredAt.Format(time.RFC3339Nano)
						r.ContextTokens, r.ContextMeasuredAt = &tokens, &at
					}
					dto.Reports = append(dto.Reports, r)
					in := &gen.WorkerTerminalReportInput{Worker: id, Status: gen.WorkerTerminalState(r.Status), Prompt: gen.WorkerTerminalPrompt(r.Prompt), CheckedAt: r.CheckedAt, LastTurnAt: r.LastTurnAt}
					if r.ContextTokens != nil && r.ContextMeasuredAt != nil {
						in.ContextTokens, in.ContextMeasuredAt = r.ContextTokens, r.ContextMeasuredAt
					}
					if r.Reason != nil {
						v := gen.WorkerTerminalReason(*r.Reason)
						in.Reason = &v
					}
					inputs = append(inputs, in)
				}
			}
			for _, id := range names {
				if !matched[id] {
					return exitcode.Newf(exitcode.Error, "terminal status did not report every registered worker; nothing written")
				}
			}
			if len(inputs) > 200 {
				return exitcode.Newf(exitcode.Error, "host report exceeds the server's 200-worker batch limit; nothing written")
			}
			if len(inputs) > 0 {
				resp, err := gen.HealthSweepSetTerminals(c.Context(), server.client, &gen.SetHostWorkerTerminalsInput{AppRef: cfg.App, Reports: inputs})
				if err != nil && api.IsUnknownInputFieldOnly(err, "WorkerTerminalReportInput", "contextTokens", "contextMeasuredAt") {
					// A server from before hadron-server#4238 refuses the whole
					// batch at validation for the unknown fields. Terminal health
					// outranks context size: send the same reports without the
					// pair, once (cli#2063).
					stripped := false
					for _, in := range inputs {
						if in.ContextTokens != nil || in.ContextMeasuredAt != nil {
							in.ContextTokens, in.ContextMeasuredAt = nil, nil
							stripped = true
						}
					}
					if stripped {
						for i := range dto.Reports {
							dto.Reports[i].ContextTokens, dto.Reports[i].ContextMeasuredAt = nil, nil
						}
						resp, err = gen.HealthSweepSetTerminals(c.Context(), server.client, &gen.SetHostWorkerTerminalsInput{AppRef: cfg.App, Reports: inputs})
					}
				}
				if err != nil {
					return healthSafeError(err)
				}
				if resp == nil || resp.SetHostWorkerTerminals == nil {
					return exitcode.Newf(exitcode.Error, "server did not confirm the health write")
				}
				confirmed := map[string]bool{}
				for _, stored := range resp.SetHostWorkerTerminals.Stored {
					if stored == nil || !matched[stored.Id] || confirmed[stored.Id] {
						return exitcode.Newf(exitcode.Error, "server returned an invalid health write result")
					}
					confirmed[stored.Id] = true
				}
				for _, r := range resp.SetHostWorkerTerminals.Refused {
					if r == nil || !matched[r.Worker] || confirmed[r.Worker] || (r.Code != "WORKER_NOT_FOUND" && r.Code != "TERMINAL_REPORT_STALE") {
						return exitcode.Newf(exitcode.Error, "server returned an invalid health write result")
					}
					confirmed[r.Worker] = true
					dto.Refused = append(dto.Refused, healthRefusal{r.Worker, r.Code})
				}
				if len(confirmed) != len(matched) {
					return exitcode.Newf(exitcode.Error, "server did not confirm every health report")
				}
			}
			// The #2017 title pass, then the #2027 duplicate phase: both run
			// after the reports are stored. Neither types into a terminal;
			// the duplicate phase never fails the sweep — its failures
			// surface as alerts — and the title pass's error, if any, is the
			// sweep's exit after the receipt is written.
			workerNames := make(map[string]string, len(names))
			for name, id := range names {
				workerNames[id] = name
			}
			renames, titleErr := healthTitles(c.Context(), cfg, workerNames, !dryRun)
			dto.Renames = append(dto.Renames, renames...)
			if len(workerNames) > 0 {
				dto.OrcaVersion = healthOrcaVersion(c.Context(), cfg)
			}
			dupes := runHealthDupes(c.Context(), f, cfg, state, server, dupesWorkers, !dryRun, f.IOStreams.ErrOut)
			dto.Fixes, dto.Alerts, dto.Skips = dupes.Fixes, dupes.Alerts, dupes.Skips
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				refused := map[string]string{}
				for _, r := range dto.Refused {
					refused[r.Worker] = r.Code
				}
				for _, r := range dto.Reports {
					reason := "-"
					if r.Reason != nil {
						reason = *r.Reason
					}
					suffix := ""
					if code := refused[r.Worker]; code != "" {
						suffix = " (refused: " + code + ")"
					}
					if _, e := fmt.Fprintf(w, "%s  %s  %s%s\n", output.SafeCell(display[r.Worker]), r.Status, reason, suffix); e != nil {
						return e
					}
				}
				if dto.OrcaVersion != nil {
					if _, e := fmt.Fprintf(w, "orca  %s\n", output.SafeCell(*dto.OrcaVersion)); e != nil {
						return e
					}
				}
				for _, r := range dto.Renames {
					verb := "would rename"
					switch {
					case r.Error != nil:
						verb = "rename failed (" + *r.Error + ")"
						if r.OrcaCode != nil {
							verb = "rename failed (" + *r.Error + "; orca: " + output.SafeCell(*r.OrcaCode) + ")"
						}
					case r.Applied:
						verb = "renamed"
					}
					if _, e := fmt.Fprintf(w, "title  %s  %s  %q -> %q\n", output.SafeCell(r.Name), verb, output.SafeCell(r.From), output.SafeCell(r.To)); e != nil {
						return e
					}
				}
				who := func(id string) string {
					if n := display[id]; n != "" {
						return n
					}
					return id
				}
				for _, fx := range dto.Fixes {
					how := "dry-run"
					if fx.Applied {
						how = "applied"
					}
					// SafeCell on fx.Subject is deliberate defense-in-depth on
					// /proc-derived text, not a server-text wrapper by
					// proximity: renderArgv (#1474) already %q-quotes control
					// runes and bounds the display at 200 bytes, so with the
					// short "pid N (base): " prefix the cell can never reach
					// the 256-rune cut — the wrap only re-neutralizes.
					if _, e := fmt.Fprintf(w, "%s  %s  %s  (%s)\n", output.SafeCell(who(fx.Worker)), fx.Action, output.SafeCell(fx.Subject), how); e != nil {
						return e
					}
				}
				for _, a := range dto.Alerts {
					if _, e := fmt.Fprintf(w, "%s  alert  %s  x%d\n", output.SafeCell(who(a.Worker)), a.Code, a.Count); e != nil {
						return e
					}
				}
				for _, s := range dto.Skips {
					if _, e := fmt.Fprintf(w, "%s  skip  %s\n", output.SafeCell(who(s.Worker)), s.Code); e != nil {
						return e
					}
				}
				return nil
			}); err != nil {
				return err
			}
			return titleErr
		}}
	c.Flags().StringVar(&configPath, "config", "", "host YAML configuration (required)")
	_ = c.MarkFlagRequired("config")
	c.Flags().DurationVar(&statusTimeout, "status-timeout", 0, "time allowed for the terminal status read (default: 5s per registered worker, at least 2m)")
	// The dry default is the shared #2017/#2027 posture: a manual sweep
	// lists what it would do, the scheduled entry (health install) passes
	// --dry-run=false to do it.
	c.Flags().BoolVar(&dryRun, "dry-run", true, "list the tab titles the sweep would rename and the duplicate agents it would stop and bare-shell tabs it would close, and do none of them (default; the status batch is still stored; the installed timer passes --dry-run=false)")
	group.AddCommand(c)
	group.AddCommand(healthInstallCmds(f)...)
	return group
}

// healthContextMeasurer is the measurement seam; tests replace it.
var healthContextMeasurer = func(measurer *contextsize.Measurer, family string, worktrees []string, roots contextsize.Roots) *contextsize.Measurement {
	return measurer.Measure(family, worktrees, roots, time.Now())
}

// healthContextRoots is where this worker's launcher writes: the worker's
// configured homes, else the sweep's own defaults.
func healthContextRoots(w hostpoller.WorkerConfig) contextsize.Roots {
	roots := contextsize.DefaultRoots()
	if w.CodexHome != "" {
		roots.CodexSessions = filepath.Join(w.CodexHome, "sessions")
	}
	if w.ClaudeConfigDir != "" {
		roots.ClaudeProjects = filepath.Join(w.ClaudeConfigDir, "projects")
	}
	return roots
}

// healthContext measures one registered worker's context size from what its
// launcher wrote on this host. nil means "not measured": an unconfigured
// worker, an interface without a measurer, or any file that cannot be proven.
func healthContext(cfg *hostpoller.Config, state *hostpoller.State, id, name string, measurer *contextsize.Measurer) *contextsize.Measurement {
	ref := cfg.RefFor(id, "", name)
	if ref == "" {
		return nil
	}
	rec := cfg.Recipient(state, ref, id)
	// Recipient already selects the explicit TUI, else the family's native
	// interface. Provider identity does not determine the transcript format.
	return healthContextMeasurer(measurer, rec.TUI, rec.Worktrees, healthContextRoots(cfg.Workers[ref]))
}

func healthSafeError(err error) error {
	code := exitcode.FromError(api.MapError(err))
	switch code {
	case exitcode.AuthRequired, exitcode.Forbidden, exitcode.Unavailable:
	default:
		code = exitcode.Error
	}
	return exitcode.Newf(code, "health sweep failed; no screen text or key is included in this diagnostic")
}
