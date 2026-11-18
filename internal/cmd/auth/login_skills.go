package auth

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmd/skill"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

func detectedLoginHosts(home string) ([]string, []string) {
	keys, names := []string{}, []string{}
	for _, h := range []struct {
		key, name, binary string
		dirs              []string
	}{
		{"claudeSkill", "Claude Code", "claude", []string{".claude"}},
		{"codexSkill", "Codex", "codex", []string{".agents", ".codex"}},
	} {
		present := false
		for _, d := range h.dirs {
			if st, err := os.Stat(filepath.Join(home, d)); err == nil && st.IsDir() {
				present = true
			}
		}
		if !present {
			_, err := loginSkillsLookPath(h.binary)
			present = err == nil
		}
		if present {
			keys = append(keys, h.key)
			names = append(names, h.name)
		}
	}
	return keys, names
}

func loginInstallCommand(server string) string {
	return "hadron auth login --install-skills --server " + cmdutil.ShellQuote(server)
}

func installLoginSkills(cmd *cobra.Command, f *cmdutil.Factory, server, token string, install, noInstall bool) (*skill.InstallReport, error) {
	if noInstall {
		return nil, nil
	}
	if !install {
		cfg, err := f.Config()
		if err != nil {
			return nil, err
		}
		if !cfg.SkillsInstallPrompt() {
			return nil, nil
		}
	}
	home, err := loginSkillsHome()
	if err != nil {
		return nil, fmt.Errorf("cannot locate your home directory: %w", err)
	}
	hosts, names := detectedLoginHosts(home)
	if len(hosts) == 0 {
		if install {
			_, err := fmt.Fprintln(f.IOStreams.ErrOut, "No supported agent hosts detected; no skills were installed.")
			return nil, err
		}
		return nil, nil
	}
	if !install && (f.JSON || !f.IOStreams.IsInputTerminal()) {
		_, err := fmt.Fprintln(f.IOStreams.ErrOut, "To install skills for detected hosts: "+loginInstallCommand(server))
		return nil, err
	}
	// Do not ask Factory for a client: HADRON_TOKEN/impersonation can name a
	// different account. All preflight and export reads use this login's token.
	client, err := api.NewClient(server, token, f.HTTPClient, api.WithCredentialSent(f.Credentials(), api.CredentialExplicit), api.WithDeliveryPending(f.DeliveryPending))
	if err != nil {
		return nil, err
	}
	enabled, err := skill.HasLoginSkills(cmd.Context(), client, hosts)
	if err != nil {
		return nil, err
	}
	if !enabled {
		_, err := fmt.Fprintln(f.IOStreams.ErrOut, "No skills are enabled for you on the detected hosts. Enable task skills in the Hadron portal, then run "+loginInstallCommand(server)+".")
		return nil, err
	}
	if !install {
		if _, err := fmt.Fprintf(f.IOStreams.ErrOut, "Hadron can install your team's skills for %s. Install now? [y/N] ", strings.Join(names, ", ")); err != nil {
			return nil, fmt.Errorf("could not show skill installation prompt: %w", err)
		}
		scanner := bufio.NewScanner(f.IOStreams.In)
		if !scanner.Scan() {
			return nil, scanner.Err()
		}
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if answer != "y" && answer != "yes" {
			return nil, nil
		}
	}
	return skill.InstallForLogin(cmd.Context(), client, home, hosts)
}

// reportLoginSkills preserves a successful login when its optional offer could
// not run. An explicit --install-skills request reports a failure to the caller.
func reportLoginSkills(cmd *cobra.Command, f *cmdutil.Factory, server, token string, install, noInstall bool) (*skill.InstallReport, error) {
	report, err := installLoginSkills(cmd, f, server, token, install, noInstall)
	if err != nil && !install {
		if _, writeErr := fmt.Fprintln(f.IOStreams.ErrOut, "note: signed in, but skill installation could not finish; run "+loginInstallCommand(server)+" to retry"); writeErr != nil {
			return report, writeErr
		}
		return report, nil
	}
	if err != nil {
		return report, exitcode.Newf(exitcode.FromError(err), "signed in, but skill installation failed: %v", err)
	}
	return report, nil
}
