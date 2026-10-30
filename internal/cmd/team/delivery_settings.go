package team

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Explicit DTO: schema regeneration must not change this JSON contract.
type deliverySettingsDTO struct {
	DarkAlarmWindowSeconds int  `json:"darkAlarmWindowSeconds"`
	BatchWindowSeconds     int  `json:"batchWindowSeconds"`
	BusyDelaySeconds       int  `json:"busyDelaySeconds"`
	StuckWindowSeconds     int  `json:"stuckWindowSeconds"`
	StandbyEnabled         bool `json:"standbyEnabled"`
	StandbyEffective       bool `json:"standbyEffective"`
	CheckPeriodSeconds     int  `json:"checkPeriodSeconds"`
	FailedChecksToOverdue  int  `json:"failedChecksToOverdue"`
}

const deliverySettingsHelp = `Workers expected to check the team channel use check period (default 600
seconds) and failed checks to overdue (default 2, minimum 1). The server adds
5 minutes of slack. The server derives worker check status; these commands
do not calculate it. When changing the period, the App manager must also set
the team ticker cadence to the same value. This command does not edit tickers.

Dark alarms use dark alarm window (default 1800 seconds, server bounds
300–86400). A task-less alarm names the worker and whether it is overdue
or has an unacknowledged wake. Acknowledging it does not end the episode.

The batch window, busy delay and stuck window are deprecated push-delivery
settings, retained for compatibility. They have no new checking consumer.
The batch window is also the ticker cadence floor: no ticker or
self-subscription may tick faster than it. Lowering it allows faster tickers.
Existing tickers keep their cadence. Raising it above a live ticker's cadence
is refused, naming the tickers to slow or delete first. Managers change this
shared floor; minimum 60 seconds, default 120. It also limits the normal wake rate.
Defaults for busy delay and stuck window are 600 and 900 seconds.

The server rejects values outside its current limits: busy delay 1–86400
seconds, stuck window 1–604800 seconds, and stuck must exceed busy plus the
batch window. It also checks the self-subscription cadence. These busy/stuck
limits and write rights describe current server behavior.

The two standby fields are server values, not a claim that standby delivery
is running. Enabling standby is currently refused until standby delivery ships.
A successful settings write does not mean anything was delivered.`

func newCmdTeamDeliverySettings(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "delivery-settings <command>", Short: "Read or change the App's delivery settings", Long: deliverySettingsHelp}
	c.AddCommand(newCmdTeamDeliverySettingsAction(f, false), newCmdTeamDeliverySettingsAction(f, true))
	return c
}

func newCmdTeamDeliverySettingsAction(f *cmdutil.Factory, set bool) *cobra.Command {
	use, short := "get", "Show the App's delivery settings"
	help := "Any App participant may read these settings.\n\n" + deliverySettingsHelp
	if set {
		use, short = "set", "Change the App's delivery settings"
		help = "Change only the fields you pass; omitted fields keep their values.\nExplicit --standby=false sends false. No null or reset values are accepted.\nWith no field flags, set returns current setting values but still requires\nmanager write permission. Use get for a read.\n\nThe server currently permits only App managers to write settings. These\ncommands use your ordinary authenticated caller, never a bound worker's\nsession or a fallback credential.\n\n" + deliverySettingsHelp
	}
	c := &cobra.Command{Use: use, Short: short, Long: help, Args: cobra.NoArgs}
	var batch, busy, stuck, checkPeriod, failedChecks, darkWindow string
	var standby bool
	if set {
		c.Flags().StringVar(&batch, "batch-window-seconds", "", "Shared ticker cadence floor and batch window, whole seconds (minimum 60); existing tickers keep their cadence; raising above a live ticker is refused")
		c.Flags().StringVar(&busy, "busy-delay-seconds", "", "Busy delay in whole seconds; the server checks its current limits")
		c.Flags().StringVar(&stuck, "stuck-window-seconds", "", "Stuck window in whole seconds; the server checks its current limits and joint relation")
		c.Flags().StringVar(&checkPeriod, "check-period-seconds", "", "Expected team-channel check period in whole seconds; coordinate it with the team ticker cadence")
		c.Flags().StringVar(&failedChecks, "failed-checks-to-overdue", "", "Missed check periods before OVERDUE (server minimum 1), plus 5 minutes slack")
		c.Flags().StringVar(&darkWindow, "dark-alarm-window-seconds", "", "Dark-worker alarm window in whole seconds (server bounds 300–86400)")
		c.Flags().BoolVar(&standby, "standby", false, "Stored standby setting (true currently refused by the server); explicit false is sent")
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		input := gen.AppDeliverySettingsInput{}
		if set {
			for _, v := range []struct {
				name, raw string
				dst       **int
			}{
				{"batch-window-seconds", batch, &input.BatchWindowSeconds},
				{"busy-delay-seconds", busy, &input.BusyDelaySeconds},
				{"stuck-window-seconds", stuck, &input.StuckWindowSeconds},
				{"check-period-seconds", checkPeriod, &input.CheckPeriodSeconds},
				{"failed-checks-to-overdue", failedChecks, &input.FailedChecksToOverdue},
				{"dark-alarm-window-seconds", darkWindow, &input.DarkAlarmWindowSeconds},
			} {
				if !cmd.Flags().Changed(v.name) {
					continue
				}
				// GraphQL Int is signed 32-bit. Ranges/relations stay server-owned.
				n, err := strconv.ParseInt(v.raw, 10, 32)
				if err != nil {
					return exitcode.Newf(exitcode.Usage, "--%s takes a whole number (maximum 2147483647); no null or default value", v.name)
				}
				value := int(n)
				*v.dst = &value
			}
			if cmd.Flags().Changed("standby") {
				input.StandbyEnabled = &standby
			}
		}
		ctx := cmd.Context()
		b, err := readBindingOrNilWithApp(ctx, f)
		if err != nil {
			return err
		}
		scope, err := resolveTeamAppScope(ctx, f, b)
		if err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		var fields *gen.AppDeliverySettingsFields
		if set {
			r, e := gen.TeamDeliverySettingsSet(ctx, client, scope.Ref, &input)
			if e != nil {
				return api.MapError(e)
			}
			if r != nil && r.SetAppDeliverySettings != nil {
				fields = &r.SetAppDeliverySettings.AppDeliverySettingsFields
			}
		} else {
			r, e := gen.TeamDeliverySettings(ctx, client, scope.Ref)
			if e != nil {
				return api.MapError(e)
			}
			if r != nil && r.AppDeliverySettings != nil {
				fields = &r.AppDeliverySettings.AppDeliverySettingsFields
			}
		}
		if fields == nil {
			return exitcode.Newf(exitcode.Error, "server returned no delivery settings")
		}
		dto := deliverySettingsDTO{
			BatchWindowSeconds: fields.BatchWindowSeconds, BusyDelaySeconds: fields.BusyDelaySeconds,
			StuckWindowSeconds: fields.StuckWindowSeconds, StandbyEnabled: fields.StandbyEnabled, StandbyEffective: fields.StandbyEffective,
			DarkAlarmWindowSeconds: fields.DarkAlarmWindowSeconds,
			CheckPeriodSeconds:     fields.CheckPeriodSeconds, FailedChecksToOverdue: fields.FailedChecksToOverdue,
		}
		label := lazyAppLabel(ctx, f, scope)
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			if _, err := fmt.Fprintf(w, "app: %s\n", label()); err != nil {
				return err
			}
			if set && !cmd.Flags().Changed("batch-window-seconds") && !cmd.Flags().Changed("busy-delay-seconds") && !cmd.Flags().Changed("stuck-window-seconds") && !cmd.Flags().Changed("standby") && !cmd.Flags().Changed("check-period-seconds") && !cmd.Flags().Changed("failed-checks-to-overdue") && !cmd.Flags().Changed("dark-alarm-window-seconds") {
				if _, err := fmt.Fprintln(w, "No setting fields supplied. Current setting values (manager-only set):"); err != nil {
					return err
				}
			}
			if set && (cmd.Flags().Changed("batch-window-seconds") || cmd.Flags().Changed("busy-delay-seconds") || cmd.Flags().Changed("stuck-window-seconds") || cmd.Flags().Changed("standby") || cmd.Flags().Changed("check-period-seconds") || cmd.Flags().Changed("failed-checks-to-overdue") || cmd.Flags().Changed("dark-alarm-window-seconds")) {
				if _, err := fmt.Fprintln(w, "Delivery settings updated."); err != nil {
					return err
				}
			}
			t := output.NewTable(w, "SETTING", "SERVER VALUE")
			t.Row("dark alarm window", fmt.Sprintf("%d s", dto.DarkAlarmWindowSeconds))
			t.Row("check period", fmt.Sprintf("%d s", dto.CheckPeriodSeconds))
			t.Row("failed checks to overdue", strconv.Itoa(dto.FailedChecksToOverdue))
			t.Row("batch window / ticker cadence floor (deprecated)", fmt.Sprintf("%d s", dto.BatchWindowSeconds))
			t.Row("busy delay (deprecated)", fmt.Sprintf("%d s", dto.BusyDelaySeconds))
			t.Row("stuck window (deprecated)", fmt.Sprintf("%d s", dto.StuckWindowSeconds))
			t.Row("standby (stored)", strconv.FormatBool(dto.StandbyEnabled))
			t.Row("standby (effective)", strconv.FormatBool(dto.StandbyEffective))
			return t.Flush()
		})
	}
	return c
}
