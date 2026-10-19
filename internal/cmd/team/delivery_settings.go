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
	BatchWindowSeconds int  `json:"batchWindowSeconds"`
	BusyDelaySeconds   int  `json:"busyDelaySeconds"`
	StuckWindowSeconds int  `json:"stuckWindowSeconds"`
	StandbyEnabled     bool `json:"standbyEnabled"`
	StandbyEffective   bool `json:"standbyEffective"`
}

const deliverySettingsHelp = `The batch window IS the ticker cadence floor: changing it changes ticker
cadence. Managers change this shared floor; it is at least 60 seconds.
Defaults: batch window/floor 120 seconds, busy delay 600, stuck window 900.

The server rejects values outside its current limits: busy delay 1–86400
seconds, stuck window 1–604800 seconds, and stuck must exceed busy plus the
batch window. It also checks existing ticker and self-subscription cadences.
These busy/stuck limits and write rights describe current server behavior.

The two standby fields are server values, not a claim that standby delivery
is running. Enabling standby is currently refused until that behavior ships
(S7). A successful settings write does not mean anything was delivered.`

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
		help = "Change only the fields you pass; omitted fields keep their values.\nExplicit --standby=false sends false. No null or reset values are accepted.\nWith no field flags, request unchanged settings readback, not a change.\n\nThe server currently permits only App managers to write settings. These\ncommands use your ordinary authenticated caller, never a bound worker's\nsession or a fallback credential.\n\n" + deliverySettingsHelp
	}
	c := &cobra.Command{Use: use, Short: short, Long: help, Args: cobra.NoArgs}
	var batch, busy, stuck string
	var standby bool
	if set {
		c.Flags().StringVar(&batch, "batch-window-seconds", "", "Shared ticker cadence floor and batch window, whole seconds (minimum 60); changing it changes ticker cadence")
		c.Flags().StringVar(&busy, "busy-delay-seconds", "", "Busy delay in whole seconds; the server checks its current limits")
		c.Flags().StringVar(&stuck, "stuck-window-seconds", "", "Stuck window in whole seconds; the server checks its current limits and joint relation")
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
			} {
				if !cmd.Flags().Changed(v.name) {
					continue
				}
				// GraphQL Int is signed 32-bit. Ranges/relations stay server-owned.
				n, err := strconv.ParseInt(v.raw, 10, 32)
				if err != nil {
					return exitcode.Newf(exitcode.Usage, "--%s takes a whole number of seconds within GraphQL Int range; no null or default value", v.name)
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
		dto := deliverySettingsDTO{fields.BatchWindowSeconds, fields.BusyDelaySeconds, fields.StuckWindowSeconds, fields.StandbyEnabled, fields.StandbyEffective}
		label := lazyAppLabel(ctx, f, scope)
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			if _, err := fmt.Fprintf(w, "app: %s\n", label()); err != nil {
				return err
			}
			if set && !cmd.Flags().Changed("batch-window-seconds") && !cmd.Flags().Changed("busy-delay-seconds") && !cmd.Flags().Changed("stuck-window-seconds") && !cmd.Flags().Changed("standby") {
				if _, err := fmt.Fprintln(w, "unchanged settings readback (empty patch)"); err != nil {
					return err
				}
			}
			t := output.NewTable(w, "SETTING", "SERVER VALUE")
			t.Row("batch window / ticker cadence floor", fmt.Sprintf("%d s", dto.BatchWindowSeconds))
			t.Row("busy delay", fmt.Sprintf("%d s", dto.BusyDelaySeconds))
			t.Row("stuck window", fmt.Sprintf("%d s", dto.StuckWindowSeconds))
			t.Row("standbyEnabled", strconv.FormatBool(dto.StandbyEnabled))
			t.Row("standbyEffective", strconv.FormatBool(dto.StandbyEffective))
			return t.Flush()
		})
	}
	return c
}
