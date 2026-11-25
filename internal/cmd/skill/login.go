package skill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

// InstallReport is the existing export report, embedded in login's JSON only
// for an explicit installation. It has the same filesystem outcomes as export.
type InstallReport = exportDTO

// Render prints the existing human export report, including its conditional
// restart hint. It never changes the installation.
func (r exportDTO) Render(w io.Writer) error { return renderExport(w, r) }

func loginHosts(keys []string) ([]skilldoc.Host, error) {
	hosts := []skilldoc.Host{}
	for _, key := range keys {
		found := false
		for _, h := range skilldoc.Hosts {
			if h.Key == key {
				hosts = append(hosts, h)
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("unsupported skill host (the value given is not shown)")
		}
	}
	return hosts, nil
}

// HasLoginSkills asks the existing planner which detected hosts have enabled
// sources. No local files are scanned or written before consent. The eventual
// write re-plans against current disk facts; this read grants no write authority.
func HasLoginSkills(ctx context.Context, client graphql.Client, keys []string) (bool, error) {
	hosts, err := loginHosts(keys)
	if err != nil {
		return false, err
	}
	enabled := false
	for _, h := range hosts {
		resp, err := gen.LoginSkillAvailability(ctx, client, &gen.SkillPlanInput{Intent: gen.SkillPlanIntentStatus, Host: &h.Key})
		if err != nil {
			return false, api.MapError(err)
		}
		if resp.SkillPlan == nil || resp.SkillPlan.Entries == nil {
			return false, fmt.Errorf("skillPlan returned no result")
		}
		for _, e := range resp.SkillPlan.Entries {
			if e == nil || e.Class == nil {
				return false, fmt.Errorf("skillPlan returned an unclassified source")
			}
			if *e.Class != "disabled" {
				enabled = true
			}
		}
	}
	return enabled, nil
}

// InstallForLogin uses the same planner, root guards, ownership checks and I/O
// as skill export, with exactly the detected hosts and no force/prune override.
// The caller passes the client authenticated by the token login just stored.
func InstallForLogin(ctx context.Context, client graphql.Client, home string, keys []string) (*InstallReport, error) {
	hosts, err := loginHosts(keys)
	if err != nil {
		return nil, err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve your home directory: %w", err)
	}
	dto, err := runExport(ctx, client, home, hosts, nil, exportOpts{}, nil)
	if err != nil {
		return nil, err
	}
	if exportHasFailures(dto) {
		return &dto, exitcode.Newf(exitcode.Conflict, "some skills could not be installed; see the export report")
	}
	return &dto, nil
}
