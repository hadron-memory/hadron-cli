package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// TestCheckUnknownSubcommand covers #232: a group command used to print
// its own help and exit 0 for a subcommand that doesn't exist.
func TestCheckUnknownSubcommand(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantErr  bool
		contains []string
	}{
		{
			name:     "unknown spec subcommand suggests edit",
			args:     []string{"spec", "update"},
			wantErr:  true,
			contains: []string{`unknown command "update" for "hadron spec"`, `did you mean "edit"?`},
		},
		{
			// --help must not launder the typo into a successful help
			// print; cobra's help branch fires before ValidateArgs.
			name:     "unknown spec subcommand with --help",
			args:     []string{"spec", "update", "--help"},
			wantErr:  true,
			contains: []string{`unknown command "update" for "hadron spec"`, `did you mean "edit"?`},
		},
		{
			name:     "spec delete points at supersede",
			args:     []string{"spec", "delete"},
			wantErr:  true,
			contains: []string{`unknown command "delete" for "hadron spec"`, `did you mean "supersede"?`},
		},
		{
			name:     "nested group is checked too",
			args:     []string{"memory", "member", "bogus"},
			wantErr:  true,
			contains: []string{`unknown command "bogus" for "hadron memory member"`},
		},
		{
			name:     "no suggestion when nothing is close",
			args:     []string{"spec", "zzzzzz"},
			wantErr:  true,
			contains: []string{`unknown command "zzzzzz" for "hadron spec"`},
		},
		{name: "bare group prints help", args: []string{"spec"}},
		{name: "group --help prints help", args: []string{"spec", "--help"}},
		{name: "valid subcommand", args: []string{"spec", "edit", "cor:acl:010"}},
		{name: "valid leaf with positional args", args: []string{"node", "get", "acme.com::kb::x"}},
		{name: "no args at all", args: nil},
		// A leaf's own positional arg must never be mistaken for a
		// subcommand, including when a flag value precedes it.
		{name: "flag value before positional", args: []string{"node", "get", "-m", "acme.com::kb", "findings:x"}},
		{name: "unknown root command is cobra's to report", args: []string{"bogus"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := checkUnknownSubcommand(tt.args)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an unknown-command error, got nil")
			}
			if got := exitcode.FromError(err); got != exitcode.Usage {
				t.Errorf("exit code = %d, want %d (usage)", got, exitcode.Usage)
			}
			for _, want := range tt.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}

// The probe parses flags to find positionals, so it must do that on a
// tree of its own — otherwise it would leave the factory that actually
// executes pre-populated from the probe's parse.
func TestCheckUnknownSubcommandDoesNotMutateFactory(t *testing.T) {
	f := cmdutil.NewFactory()
	root := NewRootCmd(f)

	if _, err := checkUnknownSubcommand([]string{"spec", "update", "--json", "--server", "https://probe.example"}); err == nil {
		t.Fatal("expected an unknown-command error")
	}
	if f.JSON {
		t.Error("probe parsing set f.JSON on the executing tree's factory")
	}
	if f.ServerFlag != "" {
		t.Errorf("probe parsing set f.ServerFlag = %q", f.ServerFlag)
	}
	// Guard against the probe reaching the live tree some other way.
	if root.Flags().Changed("json") {
		t.Error("probe parsing marked --json changed on the executing tree")
	}
}

// The probe fires before cobra binds --json, so it reports the flag
// itself — otherwise a --json caller gets a bare text error on stderr
// and the machine-readable contract breaks for this one failure.
func TestCheckUnknownSubcommandReportsJSONFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"no flag", []string{"spec", "update"}, false},
		{"after the group", []string{"spec", "update", "--json"}, true},
		{"before the group", []string{"--json", "spec", "update"}, true},
		{"explicit value", []string{"spec", "update", "--json=true"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonOut, err := checkUnknownSubcommand(tt.args)
			if err == nil {
				t.Fatal("expected an unknown-command error")
			}
			if jsonOut != tt.want {
				t.Errorf("jsonOut = %v, want %v", jsonOut, tt.want)
			}
		})
	}
}

func TestOrList(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{[]string{"edit"}, `"edit"`},
		{[]string{"edit", "extract"}, `"edit" or "extract"`},
		{[]string{"a", "b", "c"}, `"a", "b" or "c"`},
	}
	for _, tt := range tests {
		if got := orList(tt.in); got != tt.want {
			t.Errorf("orList(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
