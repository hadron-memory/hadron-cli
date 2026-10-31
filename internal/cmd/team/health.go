package team

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
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
}
type healthRefusal struct {
	Worker string `json:"worker"`
	Code   string `json:"code"`
}
type healthReceipt struct {
	Reports []healthReport  `json:"reports"`
	Refused []healthRefusal `json:"refused"`
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
		out = append(out, healthReport{r.Worker, r.Status, r.Prompt, r.Reason, r.LastTurnAt, r.CheckedAt})
	}
	return out, nil
}

func newCmdTeamHealth(f *cmdutil.Factory) *cobra.Command {
	return newCmdTeamHealthWithReader(f, readHealthStatusCommand)
}
func newCmdTeamHealthWithReader(f *cmdutil.Factory, read healthStatusReader) *cobra.Command {
	group := &cobra.Command{Use: "health", Short: "Report this host's workers' terminal health"}
	var configPath string
	c := &cobra.Command{Use: "sweep --config <host.yaml>", Short: "Read terminal status and store code-only reports for this host", Args: cobra.NoArgs,
		Long: `Read terminal-status@1 JSON from "hadron orca terminal status --all --json"
and store the status of this host's registered workers in one batch. The status
command alone classifies terminals; this binary runs its own status command,
and the sweep never reads screens or types.

Use the same --config host YAML and 0600 key_file as "team host run". The host
key is never supplied as an argument or environment variable. Orca is resolved
from delivery.orca_bin when set, otherwise PATH. Paused workers are still reported.
No alarm is computed, and an App without a coordinator is valid. An unresolved,
ambiguous or missing registered worker report refuses the entire batch before
writing (exit 1). A missing or non-live host registration is exit 8.
Status rows for workers not registered on this host are ignored.

Exit 0 after a successful write, including per-worker refusals (printed, not
retried); 1 for a failed write or malformed status contract, 3 for a missing or
rejected key, 7 when the server is unreachable, 8 for forbidden and 10 when the
status command cannot reach Orca. No scheduling or recovery is installed.
--json contains the sent reports (including refused items) and per-worker
refusals; never screen text. A report in this list is not a storage receipt.`,
		RunE: func(c *cobra.Command, _ []string) error {
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
						}
					}
				}
			}
			if !live {
				return exitcode.Newf(exitcode.Forbidden, "host registration is not live; nothing written")
			}
			raw, code, err := read(c.Context(), configPath, cfg)
			if code == 10 {
				return exitcode.Newf(exitcode.OrcaUnreachable, "Orca is not reachable on this host; nothing written")
			}
			if err != nil || code != 0 {
				if code == 3 || code == 7 || code == 8 {
					return exitcode.Newf(code, "terminal status command failed; nothing written")
				}
				return exitcode.Newf(exitcode.Error, "terminal status command failed; nothing written")
			}
			rows, err := decodeHealthStatus(raw)
			if err != nil {
				return err
			}
			dto := healthReceipt{Reports: []healthReport{}, Refused: []healthRefusal{}}
			inputs := []*gen.WorkerTerminalReportInput{}
			matched := map[string]bool{}
			display := map[string]string{}
			for _, r := range rows {
				if id, ok := names[r.Worker]; ok {
					display[id] = r.Worker
					matched[id] = true
					r.Worker = id
					dto.Reports = append(dto.Reports, r)
					in := &gen.WorkerTerminalReportInput{Worker: id, Status: gen.WorkerTerminalState(r.Status), Prompt: gen.WorkerTerminalPrompt(r.Prompt), CheckedAt: r.CheckedAt, LastTurnAt: r.LastTurnAt}
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
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
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
				return nil
			})
		}}
	c.Flags().StringVar(&configPath, "config", "", "host YAML configuration (required)")
	_ = c.MarkFlagRequired("config")
	group.AddCommand(c)
	return group
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
