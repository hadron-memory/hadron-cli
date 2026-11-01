package app

// `hadron app properties get|set|clear` (hadron-cli#1524 C2, hadron-server#3125
// S1, cor:dmo:050:02, cor:dmo:050:20): the App's own JSON object. The platform
// reads only its taskIntegrations key (cor:agt:120:01); every other key is the
// team's. The CLI is a thin client: who may read or write, the size bound and
// the taskIntegrations shape are the server's (cor:api:240:01). It never reads
// before writing: concurrency is the server's expectedRevision check.
//
// A dedicated group rather than an `app update` flag (plan A1): the write is
// its own mutation behind its own gate (whoever manages the App, never a
// worker session), and one command per mutation keeps each refusal
// attributable.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// appPropertiesDTO is the --json shape of every `app properties` verb. Both
// properties and propertiesRevision are null for a caller who may not read
// them (and properties is null when the App has none).
type appPropertiesDTO struct {
	AppID              string           `json:"appId"`
	PropertiesRevision *int             `json:"propertiesRevision"`
	Properties         *json.RawMessage `json:"properties"`
}

func newCmdProperties(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "properties",
		Short: "Read or replace an App's own properties object",
		Long: `An App's properties are one JSON object of its own (at most 16 KiB of JSON,
64 levels). The platform reads only the taskIntegrations key, which configures
the outside trackers ` + "`hadron team task find-untracked`" + ` reads; every other
key is the team's, stored as written. Never put a secret here.

Whoever may read the App reads them (null for anyone else). Only whoever
manages the App writes them, as a person: a worker session and an App key are
refused. Each write replaces the whole object; use --expected-revision with
the revision ` + "`get --json`" + ` printed to refuse a write over someone else's.`,
	}
	cmd.AddCommand(newCmdPropertiesGet(f))
	cmd.AddCommand(newCmdPropertiesSet(f))
	cmd.AddCommand(newCmdPropertiesClear(f))
	return cmd
}

func newCmdPropertiesGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <app-ref>",
		Short: "Print an App's properties object",
		Long: `Print the App's properties object, and only it, as indented JSON, or null
when the App has none or you may not read them. The text output is valid JSON,
so ` + "`> props.json`" + ` gives a file ` + "`set`" + ` takes back; control and invisible
characters inside strings are written as \uXXXX escapes, the same value.

--json is {"appId", "propertiesRevision", "properties"}: the revision is what
` + "`set --expected-revision`" + ` checks against.

Pass an App ID, hrn:app:<root>:<slug>, or the <root>:<slug> short form.`,
		Example: `  hadron app properties get acme.com:eng-team > props.json
  hadron app properties get acme.com:eng-team --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AppPropertiesGet(cmd.Context(), client, ref)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp.App == nil {
				return exitcode.Newf(exitcode.NotFound, "no App found for %q", args[0])
			}
			return writeAppProperties(f, appPropertiesDTO{AppID: resp.App.Id, PropertiesRevision: resp.App.PropertiesRevision, Properties: resp.App.Properties})
		},
	}
}

func newCmdPropertiesSet(f *cmdutil.Factory) *cobra.Command {
	var expectedRevision int
	cmd := &cobra.Command{
		Use:   "set <app-ref> <json|@file|->",
		Short: "Replace an App's properties object",
		Long: `Replace the App's whole properties object with the one given, in one call:
a JSON object, @file to read one from a file, or - to read it from stdin. The
CLI checks only that it is JSON and not null; whether it is an object, its size
and the taskIntegrations shape are the server's to judge. An empty object {}
clears, like ` + "`clear`" + `. Prints the object as stored.

--expected-revision <n> refuses the write (APP_PROPERTIES_CONFLICT, exit 5) when
the properties changed since revision n; without it the write replaces
whatever is there. Each change is recorded as an app.properties-changed team
event naming the changed top-level keys, never their values.

Refusals: FORBIDDEN (exit 8: you do not manage the App, or you are a worker
session or App key); TASK_INTEGRATION_INVALID (exit 2, names the JSON path);
PROPERTIES_NOT_AN_OBJECT, PROPERTIES_TOO_LARGE, PROPERTIES_KEY_RESERVED (exit
2); TASK_INTEGRATION_ORG_REQUIRED (exit 5: an App with no organization cannot
hold task integrations); APP_PROPERTIES_CONFLICT (exit 5).`,
		Example: `  hadron app properties set acme.com:eng-team @props.json --expected-revision 3
  hadron app properties get acme.com:eng-team | jq '.team.x = 1' | hadron app properties set acme.com:eng-team -`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			spelling := cmdutil.JSONValueSpelling{Name: "the properties value", FileExample: "@props.json", Clear: "`hadron app properties clear`"}
			value := args[1]
			if value == "-" {
				text, err := cmdutil.ReadDocumentStdin(f.IOStreams.In, f.IOStreams.IsInputTerminal(), "-", "@file")
				if err != nil {
					return err
				}
				if len(text) > cmdutil.MaxJSONValueFileBytes {
					return exitcode.Newf(exitcode.Usage, "the properties value on stdin is larger than %d bytes; the server bound is 16 KiB of JSON", cmdutil.MaxJSONValueFileBytes)
				}
				// Stdin carries the JSON itself: an @path there is never read as a file.
				if strings.HasPrefix(strings.TrimSpace(text), "@") {
					return exitcode.Newf(exitcode.Usage, "the properties value on stdin must be JSON, not an @file reference")
				}
				value = text
			}
			props, err := cmdutil.ParseJSONValue(value, spelling)
			if err != nil {
				return err
			}
			return writeSetAppProperties(cmd, f, ref, &props, revisionFlag(cmd, expectedRevision))
		},
	}
	cmd.Flags().IntVar(&expectedRevision, "expected-revision", 0, "refuse the write unless the properties are still at this revision (as get --json shows it)")
	return cmd
}

func newCmdPropertiesClear(f *cmdutil.Factory) *cobra.Command {
	var expectedRevision int
	var yes bool
	cmd := &cobra.Command{
		Use:   "clear <app-ref> --yes",
		Short: "Remove an App's properties object",
		Long: `Remove the App's whole properties object, its task integrations included,
so ` + "`team task find-untracked`" + ` has nothing to read until it is set again.
Prompts on a terminal; non-interactively --yes is required. Takes
--expected-revision like ` + "`set`" + `, and is refused the same way.`,
		Example: `  hadron app properties clear acme.com:eng-team --yes --expected-revision 4`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "the properties of App "+args[0]); err != nil {
				return err
			}
			// A nil value is sent as an explicit null: how the mutation clears.
			return writeSetAppProperties(cmd, f, ref, nil, revisionFlag(cmd, expectedRevision))
		},
	}
	cmd.Flags().IntVar(&expectedRevision, "expected-revision", 0, "refuse the clear unless the properties are still at this revision (as get --json shows it)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// revisionFlag is --expected-revision when given, else nil, so the key is
// omitted and the server checks nothing.
func revisionFlag(cmd *cobra.Command, n int) *int {
	if !cmd.Flags().Changed("expected-revision") {
		return nil
	}
	return &n
}

func writeSetAppProperties(cmd *cobra.Command, f *cmdutil.Factory, ref string, props *json.RawMessage, expectedRevision *int) error {
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	resp, err := gen.SetAppProperties(cmd.Context(), client, ref, props, expectedRevision)
	if err != nil {
		return api.MapErrorKeepingCode(err)
	}
	a := resp.SetAppProperties
	return writeAppProperties(f, appPropertiesDTO{AppID: a.Id, PropertiesRevision: a.PropertiesRevision, Properties: a.Properties})
}

// writeAppProperties prints the object alone in text (valid JSON, or null),
// and the whole DTO with --json.
func writeAppProperties(f *cmdutil.Factory, dto appPropertiesDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if dto.Properties == nil {
			_, err := fmt.Fprintln(w, "null")
			return err
		}
		text, err := output.SafeJSON(*dto.Properties)
		if err != nil {
			return exitcode.Newf(exitcode.Error, "the server returned properties that are not valid JSON")
		}
		_, err = fmt.Fprintln(w, text)
		return err
	})
}
