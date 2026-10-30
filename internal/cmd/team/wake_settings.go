package team

// `team wake-settings` — one worker's wake settings (cli#1292 over server
// #1747, cor:agt:080:03). `get` is any App participant's read; `set` is the
// worker itself, its holder or whoever manages the App (cor:agt:080:21).
// Isolation fields are projected as the server returns them; isolating and
// clearing isolation are `team isolation` (cli#911 slice B2).

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type wakeTickerDTO struct {
	ID             string  `json:"id"`
	URN            *string `json:"urn"`
	Name           string  `json:"name"`
	CadenceSeconds int     `json:"cadenceSeconds"`
	Enabled        bool    `json:"enabled"`
}

type wakeSubscriptionDTO struct {
	ID        string         `json:"id"`
	Ticker    *wakeTickerDTO `json:"ticker"`
	ViaGrant  bool           `json:"viaGrant"`
	CreatedAt string         `json:"createdAt"`
}

type wakeSettingsDTO struct {
	Worker                  taskPartyDTO          `json:"worker"`
	Mentions                string                `json:"mentions"`
	PausedUntil             *string               `json:"pausedUntil"`
	Subscriptions           []wakeSubscriptionDTO `json:"subscriptions"`
	EffectiveCadenceSeconds *int                  `json:"effectiveCadenceSeconds"`
	Dormant                 bool                  `json:"dormant"`
	Isolated                bool                  `json:"isolated"`
	IsolatedAt              *string               `json:"isolatedAt"`
	IsolatedByHandle        *string               `json:"isolatedByHandle"`
	IsolatedUntil           *string               `json:"isolatedUntil"`
	CanManageIsolation      bool                  `json:"canManageIsolation"`
}

func wakeSettingsDTOFrom(s *gen.WorkerWakeSettingsFields) wakeSettingsDTO {
	dto := wakeSettingsDTO{
		Mentions:                string(s.Mentions),
		PausedUntil:             s.PausedUntil,
		Subscriptions:           []wakeSubscriptionDTO{},
		EffectiveCadenceSeconds: s.EffectiveCadenceSeconds,
		Dormant:                 s.Dormant,
		Isolated:                s.Isolated,
		IsolatedAt:              s.IsolatedAt,
		IsolatedByHandle:        s.IsolatedByHandle,
		IsolatedUntil:           s.IsolatedUntil,
		CanManageIsolation:      s.CanManageIsolation,
	}
	if s.Worker != nil {
		n := s.Worker.Name
		dto.Worker = taskPartyDTO{ID: s.Worker.Id, Kind: "WORKER", Name: &n, Urn: s.Worker.Urn}
	}
	for _, sub := range s.Subscriptions {
		if sub == nil {
			continue
		}
		d := wakeSubscriptionDTO{ID: sub.Id, ViaGrant: sub.ViaGrant, CreatedAt: sub.CreatedAt}
		if t := sub.Ticker; t != nil {
			d.Ticker = &wakeTickerDTO{ID: t.Id, URN: t.Urn, Name: t.Name, CadenceSeconds: t.CadenceSeconds, Enabled: t.Enabled}
		}
		dto.Subscriptions = append(dto.Subscriptions, d)
	}
	return dto
}

// wakeSettingsRefusalCodes are the refusals these two calls document, kept as the
// message prefix; the exit code is the mapped one, unchanged.
var wakeSettingsRefusalCodes = []string{"FORBIDDEN", "WORKER_NOT_FOUND", "BAD_USER_INPUT"}

func wakeSettingsError(err error) error {
	mapped := api.MapError(err)
	for _, code := range wakeSettingsRefusalCodes {
		if api.HasErrorCode(err, code) {
			return exitcode.Newf(exitcode.FromError(mapped), "%s: %s", code, mapped.Error())
		}
	}
	return mapped
}

// wakeWorkerRef takes the --worker value. The two operations take no App, so
// a bare NAME cannot be resolved: only an id or a worker URN names its worker.
func wakeWorkerRef(v string) (string, error) {
	ref := strings.TrimSpace(v)
	if !workerRefIsSelfSufficient(ref) {
		return "", exitcode.Newf(exitcode.Usage,
			"pass a worker id or fully-qualified hrn:worker: URN (names are not addresses); `hadron team worker list` shows the refs, got %q", v)
	}
	return ref, nil
}

func newCmdTeamWakeSettings(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wake-settings",
		Short: "Show or change a worker's wake settings",
		Long: `A worker's wake settings (cor:agt:080:03): whether a mention wakes it at
once or not at all, an optional pause, its live ticker subscriptions and the
fastest cadence they give, and whether it is dormant or isolated. ` + "`get`" + ` shows
them for any participant of the App, with the server's defaults when the worker
has none stored; ` + "`set`" + ` changes the mention setting and the pause for the
worker itself, its holder or whoever manages the App (cor:agt:080:21), never
the may-subscribe-workers grant. Both take the worker's id or URN.`,
	}
	cmd.AddCommand(newCmdTeamWakeSettingsGet(f), newCmdTeamWakeSettingsSet(f))
	return cmd
}

func newCmdTeamWakeSettingsGet(f *cmdutil.Factory) *cobra.Command {
	var worker string
	cmd := &cobra.Command{
		Use:   "get --worker <id-or-URN>",
		Short: "Show a worker's wake settings",
		Long: `Show one worker's wake settings exactly as the server returns them: the
mention setting (IMMEDIATE or OFF), the pause, the live ticker subscriptions
and the effective cadence they give, dormant, and the isolation fields. A
worker with no stored settings shows the server's defaults.`,
		Example: `  hadron team wake-settings get --worker hrn:worker:acme.com:ops:iris
  hadron team wake-settings get --worker <worker-id> --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := wakeWorkerRef(worker)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.WorkerWakeSettings(cmd.Context(), client, ref)
			if err != nil {
				return wakeSettingsError(err)
			}
			if resp.WorkerWakeSettings == nil {
				return exitcode.Newf(exitcode.NotFound, "the server returned no wake settings for %s", ref)
			}
			dto := wakeSettingsDTOFrom(&resp.WorkerWakeSettings.WorkerWakeSettingsFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return renderWakeSettings(w, dto) })
		},
	}
	cmd.Flags().StringVar(&worker, "worker", "", "Worker ID or fully-qualified worker URN (required)")
	_ = cmd.MarkFlagRequired("worker")
	return cmd
}

func newCmdTeamWakeSettingsSet(f *cmdutil.Factory) *cobra.Command {
	var worker, mentions, pausedUntil string
	var clearPause bool
	cmd := &cobra.Command{
		Use:   "set --worker <id-or-URN> [--mentions immediate|off] [--paused-until <RFC3339>] [--clear-pause]",
		Short: "Change a worker's mention setting or pause",
		Long: `Change one worker's wake settings and print the settings the server returns.
Only the flags you give are sent: a setting you do not name is kept. --mentions
is immediate or off. --paused-until (RFC 3339) pauses the worker's wakes until
that time; --clear-pause ends a pause and cannot be combined with
--paused-until. At least one of the three is required. Who may change them is
the server's rule (cor:agt:080:21): the worker itself, its holder or whoever
manages the App.`,
		Example: `  hadron team wake-settings set --worker hrn:worker:acme.com:ops:iris --mentions off
  hadron team wake-settings set --worker <worker-id> --paused-until 2026-11-01T00:00:00Z
  hadron team wake-settings set --worker <worker-id> --clear-pause`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := wakeWorkerRef(worker)
			if err != nil {
				return err
			}
			input := gen.WorkerWakeSettingsInput{}
			if cmd.Flags().Changed("mentions") {
				m := gen.MentionWake(strings.ToUpper(strings.TrimSpace(mentions)))
				if m != gen.MentionWakeImmediate && m != gen.MentionWakeOff {
					return exitcode.Newf(exitcode.Usage, "--mentions takes immediate or off, got %q", mentions)
				}
				input.Mentions = &m
			}
			if cmd.Flags().Changed("paused-until") {
				if _, err := time.Parse(time.RFC3339, pausedUntil); err != nil {
					return exitcode.Newf(exitcode.Usage, "--paused-until takes an RFC 3339 timestamp, e.g. 2026-11-01T00:00:00Z, got %q", pausedUntil)
				}
				input.PausedUntil = &pausedUntil
			}
			if clearPause {
				if input.PausedUntil != nil {
					return exitcode.Newf(exitcode.Usage, "--clear-pause and --paused-until contradict each other: give one")
				}
				input.ClearPause = &clearPause
			}
			if input.Mentions == nil && input.PausedUntil == nil && input.ClearPause == nil {
				return exitcode.Newf(exitcode.Usage, "nothing to change: give --mentions, --paused-until or --clear-pause")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetWorkerWakeSettings(cmd.Context(), client, ref, &input)
			if err != nil {
				return wakeSettingsError(err)
			}
			if resp.SetWorkerWakeSettings == nil {
				return exitcode.Newf(exitcode.Error, "server returned no wake settings; read them with `team wake-settings get` before retrying")
			}
			dto := wakeSettingsDTOFrom(&resp.SetWorkerWakeSettings.WorkerWakeSettingsFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return renderWakeSettings(w, dto) })
		},
	}
	cmd.Flags().StringVar(&worker, "worker", "", "Worker ID or fully-qualified worker URN (required)")
	cmd.Flags().StringVar(&mentions, "mentions", "", "immediate or off")
	cmd.Flags().StringVar(&pausedUntil, "paused-until", "", "pause the worker's wakes until this RFC 3339 time")
	cmd.Flags().BoolVar(&clearPause, "clear-pause", false, "end the worker's pause (not with --paused-until)")
	_ = cmd.MarkFlagRequired("worker")
	return cmd
}

func renderWakeSettings(w io.Writer, d wakeSettingsDTO) error {
	name := "(unnamed)"
	if d.Worker.Name != nil {
		name = output.SafeCell(*d.Worker.Name)
	}
	line := func(k, v string) error { _, err := fmt.Fprintf(w, "%-24s %s\n", k+":", v); return err }
	cadence := "none"
	if d.EffectiveCadenceSeconds != nil {
		cadence = fmt.Sprintf("%d s", *d.EffectiveCadenceSeconds)
	}
	handle := "-"
	if d.IsolatedByHandle != nil {
		handle = output.SafeCell(*d.IsolatedByHandle)
	}
	for _, kv := range [][2]string{
		{"worker", name},
		{"mentions", d.Mentions},
		{"paused until", strOr(d.PausedUntil)},
		{"effective cadence", cadence},
		{"dormant", yesNo(d.Dormant)},
		{"isolated", yesNo(d.Isolated)},
		{"isolated at", strOr(d.IsolatedAt)},
		{"isolated until", strOr(d.IsolatedUntil)},
		{"isolated by", handle},
		{"you may manage isolation", yesNo(d.CanManageIsolation)},
	} {
		if err := line(kv[0], kv[1]); err != nil {
			return err
		}
	}
	if len(d.Subscriptions) == 0 {
		return line("subscriptions", "none")
	}
	t := output.NewTable(w, "SUBSCRIPTION", "TICKER", "CADENCE", "ENABLED", "VIA GRANT")
	for _, s := range d.Subscriptions {
		tk, cad, en := "-", "-", "-"
		if s.Ticker != nil {
			tk, cad, en = output.SafeCell(s.Ticker.Name), fmt.Sprintf("%d s", s.Ticker.CadenceSeconds), yesNo(s.Ticker.Enabled)
		}
		t.Row(s.ID, tk, cad, en, yesNo(s.ViaGrant))
	}
	return t.Flush()
}
