package coding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// stubSources builds a chain where every branch is silent unless a test fills
// it in — so a test that means to exercise the third source cannot accidentally
// be answered by the first.
func stubSources() memorySources {
	return memorySources{
		project:   func() projectCodingConfig { return projectCodingConfig{} },
		repoName:  func(context.Context) string { return "" },
		clientFor: func() (graphql.Client, error) { return nil, errors.New("no client wanted") },
		lookup: func(context.Context, graphql.Client, string) ([]string, error) {
			return nil, errors.New("no lookup wanted")
		},
	}
}

// The chain, in priority order — and each case asserts the SOURCE as well as
// the memory, because "it resolved" is the much less useful half
// (review:ambient-scope-must-report-its-source).
func TestResolveCodingMemoryWalksTheChainInOrder(t *testing.T) {
	for _, tc := range []struct {
		name       string
		flag       string
		src        func(memorySources) memorySources
		wantRaw    string
		wantSource memorySource
	}{
		{
			name: "the flag wins over everything below it",
			flag: "hrn:mem:acme.com:from-flag",
			src: func(s memorySources) memorySources {
				s.project = func() projectCodingConfig { return projectCodingConfig{Memory: "hrn:mem:acme.com:proj"} }
				s.cfgMemory = "hrn:mem:acme.com:cfg"
				return s
			},
			wantRaw: "hrn:mem:acme.com:from-flag", wantSource: memoryFromFlag,
		},
		{
			// coding.memory before the top-level memory: a repo whose checklist
			// lives elsewhere says so specifically, and the general key must not
			// override the specific one.
			name: "coding.memory beats the top-level memory key",
			src: func(s memorySources) memorySources {
				s.project = func() projectCodingConfig {
					return projectCodingConfig{Memory: "hrn:mem:acme.com:general", CodingMemory: "hrn:mem:acme.com:specific"}
				}
				return s
			},
			wantRaw: "hrn:mem:acme.com:specific", wantSource: memoryFromProjectConfig,
		},
		{
			name: "the project config beats the configured memory",
			src: func(s memorySources) memorySources {
				s.project = func() projectCodingConfig { return projectCodingConfig{Memory: "hrn:mem:acme.com:proj"} }
				s.cfgMemory = "hrn:mem:acme.com:cfg"
				return s
			},
			wantRaw: "hrn:mem:acme.com:proj", wantSource: memoryFromProjectConfig,
		},
		{
			name: "the configured memory beats the git remote",
			src: func(s memorySources) memorySources {
				s.cfgMemory = "hrn:mem:acme.com:cfg"
				s.repoName = func(context.Context) string { return "widget" }
				return s
			},
			wantRaw: "hrn:mem:acme.com:cfg", wantSource: memoryFromUserConfig,
		},
		{
			name: "a single name match resolves from the repository",
			src: func(s memorySources) memorySources {
				s.repoName = func(context.Context) string { return "widget" }
				s.clientFor = func() (graphql.Client, error) { return nil, nil }
				s.lookup = func(context.Context, graphql.Client, string) ([]string, error) {
					return []string{"hrn:mem:acme.com:widget"}, nil
				}
				return s
			},
			wantRaw: "hrn:mem:acme.com:widget", wantSource: memoryFromRepoName,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveCodingMemory(context.Background(), tc.src(stubSources()), tc.flag)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got.raw != tc.wantRaw {
				t.Errorf("memory = %q, want %q", got.raw, tc.wantRaw)
			}
			if got.source != tc.wantSource {
				t.Errorf("source = %v, want %v", got.source, tc.wantSource)
			}
		})
	}
}

// THE NETWORK IS NOT CONSULTED unless every local source is silent.
//
// An offline reviewer with a configured memory must not pay a round trip, and —
// the sharper half — must not be REFUSED because the server is unreachable, for
// a question the local config already answered.
func TestResolveCodingMemoryDoesNotReachTheServerWhenALocalSourceAnswers(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  func(memorySources) memorySources
		flag string
	}{
		{"flag", func(s memorySources) memorySources { return s }, "hrn:mem:acme.com:k"},
		{"project config", func(s memorySources) memorySources {
			s.project = func() projectCodingConfig { return projectCodingConfig{Memory: "hrn:mem:acme.com:k"} }
			return s
		}, ""},
		{"configured memory", func(s memorySources) memorySources {
			s.cfgMemory = "hrn:mem:acme.com:k"
			return s
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			src := tc.src(stubSources())
			src.clientFor = func() (graphql.Client, error) { reached = true; return nil, nil }
			if _, err := resolveCodingMemory(context.Background(), src, tc.flag); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if reached {
				t.Error("a local source answered; the server must not be consulted")
			}
		})
	}
}

// A FAILED LOOKUP IS NOT AN ANSWER ABOUT THE MEMORY.
//
// The git-remote branch is opportunistic. A client that will not build (no
// token) or a query that does not answer (server down) says nothing about which
// memory the reader meant, so surfacing it would hand a signed-out reviewer
// AuthRequired — and an offline one exit 7 — for a question entirely about their
// arguments. That is the defect #556 fixed by hoisting a guard above the client
// build, and it must not come back through this door.
func TestAFailedLookupBecomesAUsageRefusalNotATransportOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  func(memorySources) memorySources
	}{
		{"the client will not build", func(s memorySources) memorySources {
			s.clientFor = func() (graphql.Client, error) { return nil, errors.New("no credentials") }
			return s
		}},
		{"the query does not answer", func(s memorySources) memorySources {
			s.clientFor = func() (graphql.Client, error) { return nil, nil }
			s.lookup = func(context.Context, graphql.Client, string) ([]string, error) {
				return nil, errors.New("connection refused")
			}
			return s
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src(stubSources())
			src.repoName = func(context.Context) string { return "widget" }
			_, err := resolveCodingMemory(context.Background(), src, "")
			if err == nil {
				t.Fatal("an unresolvable memory must refuse")
			}
			if got := exitcode.FromError(err); got != exitcode.Usage {
				t.Errorf("exit code = %d, want %d (Usage) — a lookup failure must not answer with the network", got, exitcode.Usage)
			}
			// The refusal must be actionable, and must not leak the transport
			// wording that would send the reader at the wrong problem.
			for _, want := range []string{"-m", ".hadron/config.json", "widget"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must carry %q: %v", want, err)
				}
			}
			for _, forbidden := range []string{"credentials", "connection refused"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Errorf("the refusal must not report the lookup's failure as the answer (%q): %v", forbidden, err)
				}
			}
		})
	}
}

// AMBIGUOUS IS NOT UNRESOLVED — it lists the candidates.
//
// Sparing the reader an unfiltered `memory list` is the whole point of this
// branch (#551), so a refusal that does not name the shortlist sends them
// straight back to the thing the feature exists to avoid.
func TestAnAmbiguousRepoNameListsTheCandidates(t *testing.T) {
	src := stubSources()
	src.repoName = func(context.Context) string { return "widget" }
	src.clientFor = func() (graphql.Client, error) { return nil, nil }
	src.lookup = func(context.Context, graphql.Client, string) ([]string, error) {
		return []string{"hrn:mem:acme.com:widget", "hrn:mem:other.org:widget"}, nil
	}
	_, err := resolveCodingMemory(context.Background(), src, "")
	if err == nil {
		t.Fatal("two matches must refuse rather than pick one")
	}
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d (Usage)", got, exitcode.Usage)
	}
	for _, want := range []string{"hrn:mem:acme.com:widget", "hrn:mem:other.org:widget", "-m"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q so the reader can choose: %v", want, err)
		}
	}
}

// Slug equality, never a fuzzy match — the one behaviour that must not be
// "helpful". A near-match resolving silently would run the review against
// another team's checklist and look exactly like success.
func TestRepoNameMatchingIsExactOnTheSlug(t *testing.T) {
	for _, tc := range []struct {
		urn, repo string
		want      bool
	}{
		{"hrn:mem:acme.com:widget", "widget", true},
		{"hrn:mem:acme.com:widget", "WIDGET", true}, // case is not a difference
		{"acme.com::widget", "widget", true},        // legacy grammar still accepted (#239)
		{"hrn:mem:acme.com:widget", "widgets", false},
		{"hrn:mem:acme.com:widget", "wid", false},
		{"hrn:mem:acme.com:widget-app", "widget", false},
	} {
		got := strings.EqualFold(memorySlugOf(tc.urn), tc.repo)
		if got != tc.want {
			t.Errorf("%s vs repo %q: matched=%v, want %v (slug=%q)", tc.urn, tc.repo, got, tc.want, memorySlugOf(tc.urn))
		}
	}
}

func TestRepoNameFromRemoteURL(t *testing.T) {
	for _, tc := range []struct{ remote, want string }{
		{"git@github.com:micromentor-team/mm-app.git", "mm-app"},
		{"https://github.com/hadron-memory/hadron-cli.git", "hadron-cli"},
		{"https://github.com/hadron-memory/hadron-cli", "hadron-cli"},
		{"ssh://git@github.com/org/repo.git", "repo"},
		{"git@github.com:org/repo.git\n", "repo"}, // trailing newline from git
		{"/srv/git/bare-repo.git", "bare-repo"},
	} {
		m := reRemoteRepo.FindStringSubmatch(strings.TrimSpace(tc.remote))
		if m == nil {
			t.Errorf("%q matched nothing, want %q", tc.remote, tc.want)
			continue
		}
		if m[1] != tc.want {
			t.Errorf("%q → %q, want %q", tc.remote, m[1], tc.want)
		}
	}
}

// The project config is read from the working directory OR ANY ANCESTOR, so the
// command works from a subdirectory — and the walk is started from an explicit
// directory so this test does not depend on where it runs.
func TestProjectConfigIsFoundFromASubdirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".hadron"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hadron", "config.json"),
		[]byte(`{"memory":"hrn:mem:acme.com:widget","coding":{"memory":"hrn:mem:acme.com:checks"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "internal", "cmd")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got := projectCodingConfigFrom(deep)
	if got.Memory != "hrn:mem:acme.com:widget" || got.CodingMemory != "hrn:mem:acme.com:checks" {
		t.Errorf("read %+v, want both keys from the ancestor's config", got)
	}

	// A MALFORMED file is not an error — it falls through to the next source.
	// Failing here would make an unrelated typo in a shared config file break
	// every coding command in the repo, including ones passing -m.
	bad := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bad, ".hadron"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, ".hadron", "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := projectCodingConfigFrom(bad); got != (projectCodingConfig{}) {
		t.Errorf("a malformed config must yield the zero value, got %+v", got)
	}
}
