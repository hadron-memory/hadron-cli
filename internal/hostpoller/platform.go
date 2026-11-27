package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/hostfacts"
)

// A host that runs from the platform (hadron-cli#3237, C3 of hadron-server#5209: `host run` with no --config).
// The platform states, the host confirms: each poll the host reads its own platform row (agentTeamHost) and its
// placements (hostSetup under the App's SERVER setup source), finds what is on this machine (hostfacts: Orca's
// repos and worktrees, the agent programs), and builds a Config from both, stored with the same live pointer a
// file reload uses, so registration, delivery, relaunch and a removed worker's end keep working unchanged.
//
// A workdir token is registered only when it resolves to exactly one Orca worktree; an unresolved or ambiguous
// one is left out (so ASSIGNMENT_IN_EFFECT stays false on the server) and a start for it fails with its code.

// defaultPlatformPollInterval is a platform row's null poll interval (hadron-server V1: null = 30).
const defaultPlatformPollInterval = 30

// Assignment is one worker the platform placed on this host (hostSetup.assignments).
type Assignment struct {
	WorkerID   string
	WorkerName string
	Workdir    string
	Family     string
	Model      string
	Effort     string
	Account    string
}

// PlatformSettings is this host's platform row (agentTeamHost). A nil field is the platform's default.
type PlatformSettings struct {
	HostID              string
	Label               string
	PollIntervalSeconds *int
	DeliveryEnabled     *bool
}

// PlatformFacts is what this machine has, read at the poll (hostfacts).
type PlatformFacts struct {
	Repos     []hostfacts.Repo
	Worktrees []hostfacts.Worktree
	OrcaBin   string
	// HostDir is <state dir>/hosts/<hostId> (config.EnsureHostStateDir): the poller's state file and the
	// workers' disk temp live under it.
	HostDir string
}

// BuildPlatformConfig is the Config a platform poll runs with. It never fails: what cannot be resolved is left
// out of the registration and named on the start that needs it.
func BuildPlatformConfig(app string, set PlatformSettings, assignments []Assignment, f PlatformFacts) *Config {
	c := &Config{
		App:                 app,
		Label:               set.Label,
		PollIntervalSeconds: defaultPlatformPollInterval,
		StateFile:           filepath.Join(f.HostDir, "hadron-host-state.json"),
		Workers:             map[string]WorkerConfig{},
		Repos:               map[string]string{},
		Accounts:            map[string]AccountProfile{},
		Capabilities:        []string{},
		platform:            true,
		paths:               map[string]string{},
		unresolved:          map[string]string{},
	}
	if set.PollIntervalSeconds != nil && *set.PollIntervalSeconds >= 1 && *set.PollIntervalSeconds <= 86400 {
		c.PollIntervalSeconds = *set.PollIntervalSeconds
	}
	// delivery_ack_mixed is a product default with no platform column (#5210).
	c.Delivery = DeliveryConfig{Enabled: set.DeliveryEnabled == nil || *set.DeliveryEnabled, AckMixed: true, OrcaBin: f.OrcaBin}
	c.OrcaLaunch.OrcaBin = f.OrcaBin
	for _, w := range f.Worktrees {
		if w.Archived {
			continue
		}
		for _, r := range f.Repos {
			if r.ID == w.RepoID {
				c.Repos[r.DisplayName] = r.Path
			}
		}
	}
	for _, a := range assignments {
		w := WorkerConfig{
			Family:    a.Family,
			LaunchVia: "orca",
			Profile:   &LauncherProfile{Family: a.Family, Mode: ModeLong, Model: a.Model, Effort: a.Effort, Account: a.Account},
			DiskTmp:   filepath.Join(f.HostDir, "tmp", a.WorkerID),
			Worktrees: []string{},
		}
		path, cands, res := hostfacts.ResolveWorkdir(a.Workdir, f.Repos, f.Worktrees)
		switch res {
		case hostfacts.Found:
			w.Worktrees = append(w.Worktrees, a.Workdir)
			c.paths[a.Workdir] = path
		case hostfacts.Ambiguous:
			c.unresolved[a.Workdir] = fmt.Sprintf("%s: workdir %q matches %d Orca worktrees on this host (%s); archive or rename all but one so the token names one tree; nothing was launched",
				hostfacts.CodeWorkdirAmbiguousOnHost, a.Workdir, len(cands), strings.Join(cands, ", "))
		default:
			c.unresolved[a.Workdir] = fmt.Sprintf("%s: workdir %q is not an Orca worktree on this host (it is <repo>/<worktree folder>); create it with Orca, or assign a workdir this host has; nothing was launched",
				hostfacts.CodeWorkdirNotOnHost, a.Workdir)
		}
		c.Workers[a.WorkerID] = w
	}
	return c
}

// WorkdirRefusal is the coded reason a start for token fails on a platform host whose facts did not resolve
// it ("" when they did, or under --config).
func (c *Config) WorkdirRefusal(token string) string {
	if !c.platform {
		return ""
	}
	if r, ok := c.unresolved[token]; ok {
		return r
	}
	if c.paths[token] == "" {
		return fmt.Sprintf("%s: workdir %q is not an Orca worktree on this host; nothing was launched", hostfacts.CodeWorkdirNotOnHost, token)
	}
	return ""
}

// FromPlatform reports a Config built from the platform rather than loaded from a file.
func (c *Config) FromPlatform() bool { return c.platform }

// AssignmentProfiles is the ProfileSource of a host that runs from the platform. A worker's profile is its
// assignment's family, model, effort and account NAME; the account is resolved at the moment of use
// (AccountLookup.Resolve). As a ProfileSource (a relaunch, with no start) it resolves the assignment's own
// account; as an AccountResolving source a start's settings come first (cor:agt:070:08): its family, and its
// account name when it names one, else the assignment's. There is no fallback from an unknown start account
// to the assignment's.
type AssignmentProfiles struct {
	Get    func() *Config
	Lookup AccountLookup
}

func (s AssignmentProfiles) Profile(ref string) (LauncherProfile, AccountProfile, bool, error) {
	w, ok := s.Get().Worker(ref)
	if !ok || w.Profile == nil {
		return LauncherProfile{}, AccountProfile{}, false, nil
	}
	a, err := s.Lookup.Resolve(w.Profile.Family, w.Profile.Account)
	if err != nil {
		return LauncherProfile{}, AccountProfile{}, false, err
	}
	return *w.Profile, a, true, nil
}

func (s AssignmentProfiles) ResolveProfile(ref string, rs RunSettings) (LauncherProfile, AccountProfile, error) {
	w, ok := s.Get().Worker(ref)
	if !ok || w.Profile == nil {
		return LauncherProfile{}, AccountProfile{}, fmt.Errorf("the worker has no assignment on this host")
	}
	prof := *w.Profile
	if rs.Account != "" {
		prof.Account = rs.Account
	}
	fam := prof.Family
	if rs.Family != "" {
		fam = rs.Family
	}
	a, err := s.Lookup.Resolve(fam, prof.Account)
	if err != nil {
		return LauncherProfile{}, AccountProfile{}, err
	}
	return prof, a, nil
}

// ErrSetupSourceHost is a platform read whose App's setup source is HOST: hostSetup then lists the host's own
// registration, not the platform's placements (cor:agt:070:01, :04), so there is nothing to run from. At start
// `host run` refuses it (exit 5, Ruling Q1 (a)); mid-run the poller registers nothing and waits for SERVER.
var ErrSetupSourceHost = errors.New("the App's host setup source is HOST")

// SetPlatformRefresh makes this a poller that runs from the platform: every Step calls refresh for the Config
// instead of reloading a file, and the state's trees are only the ones each poll resolved. Call before Run.
func (p *Poller) SetPlatformRefresh(refresh func(ctx context.Context) (*Config, error)) {
	p.refresh = refresh
	p.state.SetFromPlatform()
}

// platformRefresh swaps in this poll's Config. hold is true when the App's source is HOST: nothing is
// registered or collected this poll, and that is said once per flip, not every poll. Any other failure ends
// the Step (Fatal decides whether the poller stops); the Config in force stays.
func (p *Poller) platformRefresh(ctx context.Context) (hold bool, err error) {
	next, err := p.refresh(ctx)
	if errors.Is(err, ErrSetupSourceHost) {
		if !p.sourceHostSaid {
			p.sourceHostSaid = true
			p.log("setup-source-host", "", "", "", "the App's host setup source is HOST, so the platform places no workers on this host; nothing is registered until a manager switches it to SERVER (hadron team host setup --source SERVER)")
		}
		return true, nil
	}
	if err != nil {
		// The refresh's own errors say what was read and for which host (hadron-cli#3301 BLIND P3-14).
		return false, err
	}
	if p.sourceHostSaid {
		p.sourceHostSaid = false
		p.log("setup-source-server", "", "", "", "the App's host setup source is SERVER again: running from its placements")
	}
	c := p.config()
	added, removed, modified := diffWorkers(c, next)
	if len(added)+len(removed) > 0 {
		p.noteRemoved(c, removed, added)
	}
	p.live.Store(next)
	if len(added)+len(removed)+len(modified) > 0 {
		p.log("assignments-changed", "", "", "", reloadDetail(added, removed, modified, nil))
	}
	return false, nil
}
