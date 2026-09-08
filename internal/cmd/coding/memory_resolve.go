package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Resolving `coding`'s memory from the repository instead of demanding -m (#551).
//
// The friction the issue measured: a reviewer standing in a checkout had to run
// `memory list`, scan a cross-organization result, and infer the memory from the
// git remote by eye — four steps before the checklist could be read at all. The
// repository already knows which memory it belongs to; nothing was asking it.
//
// FOUR sources, in priority order, and which one answered is part of the output
// rather than an implementation detail — see reportMemorySource and
// review:ambient-scope-must-report-its-source. A command that picks its scope
// from a chain and renders the result identically whichever branch won gives the
// reader nothing to be suspicious of.

// memorySource names the branch that answered. The zero value is deliberately
// the "nothing answered" case, so a forgotten assignment reads as unresolved
// rather than silently claiming the flag was passed.
type memorySource int

const (
	memoryUnresolved memorySource = iota
	memoryFromFlag
	memoryFromProjectConfig
	memoryFromUserConfig
	memoryFromRepoName
)

// String is the phrase the render shows in parentheses. An explicit flag is
// distinguished from every ambient source, because one is something the reader
// just typed and the others are as ambient as a worktree binding.
func (s memorySource) String() string {
	switch s {
	case memoryFromFlag:
		return "from -m"
	case memoryFromProjectConfig:
		return "from .hadron/config.json"
	case memoryFromUserConfig:
		return "from the configured memory"
	case memoryFromRepoName:
		return "matched from the git remote"
	default:
		return "unresolved"
	}
}

// projectCodingConfig is the coding slice of the repo-local .hadron/config.json.
//
// Two keys are read, most specific first: `coding.memory` for a repo whose
// checklist lives somewhere other than its main memory, and the top-level
// `memory` as the ordinary "this repository's Hadron memory". The file is shared
// with `chat` and the hadron-client push channel, so this only ADDS keys and
// ignores everything else.
type projectCodingConfig struct {
	Memory       string `json:"memory"`
	CodingMemory string
	// Path and Err record a file that EXISTS but does not parse. Absent and
	// malformed are different answers: absent means this source has nothing to
	// say, malformed means it was trying to say something and could not.
	Path string
	Err  error
}

// loadProjectCodingConfig reads .hadron/config.json from the working directory
// or any ancestor, mirroring chat's loadProjectChat so one repo layout serves
// both. A MISSING file yields the zero value and never an error: this is a
// convenience layered under the flag, and a repo without one must fall through
// to the next source rather than failing.
//
// A file that exists and does NOT PARSE is a different answer, and is carried
// as one (@codex on #561). Treating it as absent silently advances to the
// configured memory or the repository name — so a `review create` could WRITE
// to a memory the repository configuration was trying to prevent, which is the
// same defect as the swallowed global config one branch below, and I fixed that
// one and left this one.
func loadProjectCodingConfig() projectCodingConfig {
	dir, err := os.Getwd()
	if err != nil {
		return projectCodingConfig{}
	}
	return projectCodingConfigFrom(dir)
}

// projectCodingConfigFrom is the searchable half, taking the directory to start
// from so tests can drive it without depending on where they happen to run.
//
// That matters more than it looks: the walk continues to the filesystem ROOT, so
// a stray .hadron/config.json in a home directory would answer for every
// repository under it — and, in a test, would silently change which branch of
// the chain the assertions were exercising.
func projectCodingConfigFrom(dir string) projectCodingConfig {
	for {
		path := filepath.Join(dir, ".hadron", "config.json")
		if raw, readErr := os.ReadFile(path); readErr == nil {
			var c struct {
				Memory string `json:"memory"`
				Coding struct {
					Memory string `json:"memory"`
				} `json:"coding"`
			}
			if err := json.Unmarshal(raw, &c); err != nil {
				return projectCodingConfig{Path: path, Err: err}
			}
			return projectCodingConfig{Memory: c.Memory, CodingMemory: c.Coding.Memory}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return projectCodingConfig{}
		}
		dir = parent
	}
}

// reRemoteRepo pulls the repository NAME out of a git remote URL, for both the
// scp-like (git@host:org/repo.git) and URL (https://host/org/repo.git) forms.
// The name is the last path segment with any .git suffix removed.
var reRemoteRepo = regexp.MustCompile(`([^/:]+?)(?:\.git)?/?\s*$`)

// repoNameFromGit returns the current repository's name, or "" when there is no
// remote to read. Best-effort by construction: no git, no repo, no remote and a
// detached checkout all mean "this source cannot answer", which is a fall-through
// and not a failure.
func repoNameFromGit(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	m := reRemoteRepo.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return ""
	}
	return m[1]
}

// resolvedMemory is a memory plus the branch that produced it.
type resolvedMemory struct {
	codingMemory
	source memorySource
}

// resolveCodingMemory walks the chain. clientFor is called ONLY if the local
// sources cannot answer — the git-remote branch has to ask the server which
// memories exist, and an offline reviewer with a configured memory should never
// pay for that (nor be refused when the network is down).
type memorySources struct {
	cfgMemory string
	// cfgErr is why cfgMemory is empty, when the global config could not be
	// read at all. Kept rather than swallowed: an unreadable TOML makes this
	// branch UNANSWERABLE, not empty, and the two must not look the same.
	cfgErr    error
	project   func() projectCodingConfig
	repoName  func(context.Context) string
	clientFor func() (graphql.Client, error)
	lookup    func(context.Context, graphql.Client, string) ([]string, error)
}

// defaultMemorySources wires the real filesystem, git and server.
func defaultMemorySources(cfgMemory string, cfgErr error, clientFor func() (graphql.Client, error)) memorySources {
	return memorySources{
		cfgMemory: cfgMemory,
		cfgErr:    cfgErr,
		project:   loadProjectCodingConfig,
		repoName:  repoNameFromGit,
		clientFor: clientFor,
		lookup:    memoriesNamed,
	}
}

func resolveCodingMemory(ctx context.Context, src memorySources, flag string) (resolvedMemory, error) {
	if v := strings.TrimSpace(flag); v != "" {
		return resolvedMemory{newCodingMemory(v), memoryFromFlag}, nil
	}
	proj := src.project()
	// Raised only AFTER the flag, and only because the chain now needs this
	// branch: a caller who passed -m must not be stopped by an unrelated typo in
	// a shared file, which is what makes "fall through on absent, refuse on
	// unparseable" the right pair rather than either alone.
	if proj.Err != nil {
		return resolvedMemory{}, exitcode.Newf(exitcode.Usage,
			"%s exists but does not parse (%v), so this repository's memory cannot be read.\n"+
				"Repair it, or pass -m hrn:mem:<root>:<slug>", proj.Path, proj.Err)
	}
	for _, v := range []string{proj.CodingMemory, proj.Memory} {
		if v = strings.TrimSpace(v); v != "" {
			return resolvedMemory{newCodingMemory(v), memoryFromProjectConfig}, nil
		}
	}
	if v := strings.TrimSpace(src.cfgMemory); v != "" {
		return resolvedMemory{newCodingMemory(v), memoryFromUserConfig}, nil
	}
	// The chain has now REACHED the global-config branch and found nothing. If
	// the reason is that the config could not be read, say so: the reader has a
	// corrupt file to repair, and telling them to "configure a memory" sends
	// them to write into the very file that is broken (@codex on #561).
	//
	// Checked here rather than at load time on purpose — a malformed config is
	// irrelevant to a caller who passed -m or has a project config, and
	// refusing them would be a failure imported from a branch nobody used.
	if src.cfgErr != nil {
		return resolvedMemory{}, exitcode.Newf(exitcode.Usage,
			"could not read your Hadron config, so there is no configured memory to fall back on: %v.\n"+
				"Repair it, or pass -m hrn:mem:<root>:<slug>", src.cfgErr)
	}

	repo := src.repoName(ctx)
	if repo == "" {
		return resolvedMemory{}, unresolvedMemoryError("")
	}
	// THE LOOKUP IS OPPORTUNISTIC, SO ITS FAILURE IS NOT THE ANSWER.
	//
	// A client that will not build (no token) or a query that does not answer
	// (server down) says nothing about which memory the reader meant. Returning
	// that error would hand a signed-out reviewer `AuthRequired`, and an offline
	// one exit 7, for a question entirely about their arguments — the same
	// defect #556 fixed by hoisting a guard above the client build, and the one
	// `alreadyBoundError` records the mirror of.
	//
	// Nothing is hidden by falling through: if the network really is the
	// problem, the reader passes -m and meets the identical failure one line
	// later, at the read that actually needed it. What they get here is the
	// question they can act on now.
	client, err := src.clientFor()
	if err != nil {
		return resolvedMemory{}, unresolvedMemoryError(repo)
	}
	matches, err := src.lookup(ctx, client, repo)
	if err != nil {
		return resolvedMemory{}, unresolvedMemoryError(repo)
	}
	switch len(matches) {
	case 1:
		return resolvedMemory{newCodingMemory(matches[0]), memoryFromRepoName}, nil
	case 0:
		return resolvedMemory{}, unresolvedMemoryError(repo)
	default:
		// AMBIGUOUS IS NOT UNRESOLVED, and it gets the candidates rather than
		// the generic remedy (#551): the whole point of this branch is to spare
		// the reader an unfiltered `memory list`, so refusing without showing
		// the shortlist would send them straight back to it.
		return resolvedMemory{}, exitcode.Newf(exitcode.Usage,
			"repository %q matches %d memories — pass -m to choose:\n  %s",
			repo, len(matches), strings.Join(matches, "\n  "))
	}
}

// unresolvedMemoryError names every source that was tried, in order, so the
// remedy is a choice rather than a guess. repo is the repository name the
// git-remote branch looked for, empty when there was no remote to read.
func unresolvedMemoryError(repo string) error {
	tried := "no -m, no `memory` or `coding.memory` in .hadron/config.json, no configured memory"
	if repo == "" {
		tried += ", and no git remote to match a memory name against"
	} else {
		tried += fmt.Sprintf(", and no accessible memory is named %q", repo)
	}
	return exitcode.Newf(exitcode.Usage,
		"could not tell which memory to use: %s.\n"+
			"(Repository matching looks only at memories you own or that are shared with you — "+
			"a PUBLIC memory in another organization is readable but is not assumed to be this repo's; name it with -m.)\n"+
			"Pass -m hrn:mem:<root>:<slug>, or set one for this repository:\n"+
			"  mkdir -p .hadron && echo '{\"memory\":\"hrn:mem:<root>:<slug>\"}' > .hadron/config.json\n"+
			"or globally with `hadron config set memory hrn:mem:<root>:<slug>`", tried)
}

// The two generated item types for the two readable slices.
type (
	codingListedMemory = gen.MemoriesMemoriesMemoriesPageItemsMemory
	codingSharedMemory = gen.MemoriesSharedWithMeMemoriesMemoriesPageItemsMemory
)

// memoriesNamed returns the URNs of accessible memories whose SLUG equals repo,
// case-insensitively.
//
// SLUG EQUALITY, NOT A FUZZY SCORE. A near-match that resolves silently is the
// one failure this chain must not have: it would run the review against another
// team's checklist and look exactly like success — a wrong answer with no
// artifact to be suspicious of, which is the defect
// review:ambient-scope-must-report-its-source exists for. Anything short of an
// exact name match falls through to a refusal that shows the candidates and
// lets the reader choose.
//
// Paged to exhaustion (the server caps a page at 200): a match sitting on page
// two of somebody's memory list would otherwise report "no such memory", which
// is issue #23's shape and reads as a settled fact.
func memoriesNamed(ctx context.Context, client graphql.Client, repo string) ([]string, error) {
	owned, err := api.CollectAll(func(limit, offset int) ([]*codingListedMemory, int, error) {
		resp, err := gen.Memories(ctx, client, nil, &limit, &offset)
		if err != nil {
			return nil, 0, api.MapError(err)
		}
		if resp == nil || resp.Memories == nil {
			return nil, 0, nil
		}
		return resp.Memories.Items, resp.Memories.Total, nil
	})
	if err != nil {
		return nil, err
	}
	// BOTH readable slices, because `memories()` is not all of them (@codex on
	// #561). A memory granted directly through `memory share` lives in the
	// separate `sharedWithMe` set — `memory list` reads the two and unions them,
	// and reading only the first here would report "no accessible memory is
	// named X" to a caller who can read X perfectly well, then demand -m for it.
	//
	// That is the sharp end of this resolver: the failure is a REFUSAL to a
	// caller with access, phrased as though the memory did not exist.
	//
	// PUBLIC memories are deliberately NOT a third slice here, though they are a
	// third readable set (@codex raised it; declined with reason). This branch
	// answers "which memory is THIS REPOSITORY's", and a public memory belonging
	// to an unrelated organisation is readable without being the answer — a sole
	// public match on a common slug would resolve silently to a stranger's
	// checklist, which is the one outcome this resolver must never produce.
	// Readability is not ownership. The refusal names -m, and passing it reads a
	// public memory perfectly well.
	shared, err := api.CollectAll(func(limit, offset int) ([]*codingSharedMemory, int, error) {
		resp, err := gen.MemoriesSharedWithMe(ctx, client, &limit, &offset)
		if err != nil {
			return nil, 0, api.MapError(err)
		}
		if resp == nil || resp.Memories == nil {
			return nil, 0, nil
		}
		return resp.Memories.Items, resp.Memories.Total, nil
	})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	add := func(urn string) {
		if urn == "" || seen[urn] || !strings.EqualFold(memorySlugOf(urn), repo) {
			return
		}
		seen[urn] = true
		out = append(out, urn)
	}
	for _, m := range owned {
		if m != nil {
			add(m.Urn)
		}
	}
	// De-duplicated across the slices: a memory can appear in both, and counting
	// it twice would turn a single clean match into a spurious "ambiguous".
	for _, m := range shared {
		if m != nil {
			add(m.Urn)
		}
	}
	sort.Strings(out)
	return out, nil
}

// memorySlugOf returns the slug of a memory URN in either grammar — the v2 flat
// `hrn:mem:<root>:<slug>` the server now emits, and the legacy `<org>::<slug>`
// still accepted everywhere (#239). Read off the ref rather than the server
// because a listing already carries it; the URN's own last segment IS the slug
// in both spellings, so there is nothing to infer.
func memorySlugOf(urn string) string {
	if urn == "" {
		return ""
	}
	if i := strings.LastIndex(urn, "::"); i >= 0 {
		return urn[i+2:]
	}
	if i := strings.LastIndex(urn, ":"); i >= 0 {
		return urn[i+1:]
	}
	return urn
}

// codingScope is what every `coding` subcommand calls in place of the old
// required-flag check: it resolves the memory and puts the resolution in the
// reader's path.
func codingScope(cmd *cobra.Command, f *cmdutil.Factory, flag string) (codingMemory, error) {
	// AN EMPTY -m IS NOT AN ABSENT -m
	// (review:an-empty-flag-is-not-an-absent-flag).
	//
	// Cobra records `-m ""` / `--memory=` as CHANGED with an empty value, so a
	// bare emptiness test hands "asked for nothing" the answer meant for "did
	// not ask" — and here that answer is a DIFFERENT MEMORY, resolved from the
	// repository, reported as if the reader had wanted it. Before #551 the same
	// input was refused; the fallback is what turns it into a silent
	// substitution, so the guard arrives with the fallback.
	//
	// `Changed` is the discriminator: it is the only thing that can tell an
	// empty value from an absent flag, which is exactly why the emptiness test
	// alone cannot.
	rm, err := codingScopeDetailed(cmd, f, flag)
	if err != nil {
		return codingMemory{}, err
	}
	return rm.codingMemory, nil
}

// codingScopeDetailed is codingScope for a caller that needs the SOURCE too —
// `review run` reports it in --json, where the response is an object and there
// is somewhere to put it. Everything else takes the memory alone and reads the
// source off stderr.
func codingScopeDetailed(cmd *cobra.Command, f *cmdutil.Factory, flag string) (resolvedMemory, error) {
	if cmd.Flags().Changed("memory") && strings.TrimSpace(flag) == "" {
		return resolvedMemory{}, exitcode.Newf(exitcode.Usage,
			"-m/--memory was given an empty value — omit it to resolve the memory from this repository, or pass hrn:mem:<root>:<slug>")
	}
	var cfgMemory string
	cfg, cfgErr := f.Config()
	if cfgErr == nil && cfg != nil {
		cfgMemory = cfg.Memory()
	}
	rm, err := resolveCodingMemory(cmd.Context(), defaultMemorySources(cfgMemory, cfgErr, f.GraphQLClient), flag)
	if err != nil {
		return resolvedMemory{}, err
	}
	rm = canonicalizeMemory(cmd.Context(), f.GraphQLClient, rm)
	reportMemorySource(f, rm)
	return rm, nil
}

// canonicalizeMemory turns an opaque memory ID into its URN, because half this
// package addresses nodes by composing one (@codex on #561).
//
// `memory set-active` documents that it stores "a memory URN OR ID", and
// `-m` accepts an id too — but `codingMemory.nodeRef` composes
// `hrn:node:<root>:<slug>:<loc>`, and `MemoryParts` cannot decompose an id, so
// every one of those callers fails with "must name a memory as hrn:mem:…". That
// message is confusing from -m and absurd from the CONFIGURED memory, where the
// reader passed nothing at all and the advertised fallback simply does not work.
//
// One round trip, and ONLY for a ref that cannot be decomposed locally — the
// ordinary URN spellings never pay for it.
// It returns NO error by design: every failure path degrades to the ref already
// in hand, so this can turn one good error into a different good error but can
// never turn a working command into a broken one.
func canonicalizeMemory(ctx context.Context, clientFor func() (graphql.Client, error), rm resolvedMemory) resolvedMemory {
	if _, _, ok := cmdutil.MemoryParts(rm.raw); ok {
		return rm // already a decomposable spelling
	}
	client, err := clientFor()
	if err != nil {
		return rm
	}
	resp, err := gen.GetMemory(ctx, client, rm.Ref)
	if err != nil || resp == nil || resp.Memory == nil || resp.Memory.Urn == "" {
		return rm
	}
	return resolvedMemory{newCodingMemory(resp.Memory.Urn), rm.source}
}

// reportMemorySource puts the resolved memory and the branch that produced it
// in front of the reader, on stderr, BEFORE the payload
// (review:ambient-scope-must-report-its-source).
//
// stderr rather than stdout so it survives `--json` without touching the
// contract: `coding review list --json` is a top-level ARRAY, and giving it a
// scope field would mean array → object, which is a break for every agent
// already parsing it. A human sees the line; a pipeline does not have to care;
// an agent that wants it reads stderr.
//
// It prints for every AMBIENT source and stays quiet for -m. The check exists
// because the same bare command in two checkouts otherwise does different things
// with visually identical output — and that reasoning does not apply to a value
// the reader typed on the line they are looking at. Nothing here is gated on
// having ROWS: an empty checklist still says which memory was empty, which is
// the case the node calls the worst one.
func reportMemorySource(f *cmdutil.Factory, rm resolvedMemory) {
	if rm.source == memoryFromFlag || rm.source == memoryUnresolved {
		return
	}
	fmt.Fprintf(f.IOStreams.ErrOut, "using memory %s (%s)\n", rm.raw, rm.source)
}
