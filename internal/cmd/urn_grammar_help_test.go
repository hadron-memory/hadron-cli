package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// #372: the CLI ACCEPTS every URN spelling forever (#239) but must only ever
// ADVERTISE grammar v2. Getting this wrong is not cosmetic — a user or an agent
// who follows `--help` writes what it shows, so a stale string propagates v1
// into other repos. hadron-portal made fully-qualified `hrn:` refs mandatory
// (hadron-portal#728) and its first CLI examples came out v1 precisely because
// that is what `--help` advertised; two review bots and a follow-up commit.
//
// Documenting a legacy form AS legacy is fine and stays — the acceptance
// promise is real and worth stating. What is banned is presenting v1 as the
// expected spelling, so a line is exempt when it says so in words.
var (
	v1Grammar = regexp.MustCompile(
		// placeholder forms: <org>::<memory>, <org::memory>, org::memory, <org>::<slug>
		`<org>::<\w+>|<org::\w+>|\borg::\w+` +
			// a concrete legacy example: acme.com::kb
			`|\b[a-z0-9-]+\.[a-z]{2,}::` +
			// mixed grammar — a v2 scheme with a v1 separator, which is simply invalid
			`|hrn:(?:mem|node|app|agent|edge|worker|org):[^\s` + "`" + `]*::` +
			// The scheme-less single-colon NODE form. Not legacy — invalid: a loc
			// carries its own single colons, so <org>:<memory>:<loc> is ambiguous
			// and refused (exit 2). It shipped in the use-hadron-cli skill as THE
			// advertised node reference, so every example an agent copied from it
			// failed (PR #454 review). Three atoms with a dotted root and no
			// hrn:/urn: prefix is the signature; two atoms are a legal memory ref.
			`|(?:^|[\s(` + "`" + `])<org>:<memory>:<loc>` +
			`|(?:^|[\s(` + "`" + `])[a-z0-9-]+\.[a-z]{2,}:[a-z0-9_-]+:[a-z0-9_:-]+`)
	// Two kinds of line legitimately SHOW a non-canonical form: one documenting
	// a legacy spelling as legacy (the acceptance promise is real and worth
	// stating), and one warning that a form is ambiguous or refused. Both are
	// teaching the reader something true; neither presents v1 as the spelling
	// to use. Each says so in words, which is what exempts it.
	marksAsNonCanonical = regexp.MustCompile(
		`(?i)legacy|accepted|ambiguous|rejected|refused|invalid|not a valid|exit 2`)
)

func v1Offenders(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if v1Grammar.MatchString(line) && !marksAsNonCanonical.MatchString(line) {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

// Every string the command tree can print: Short, Long, Example, and each
// flag's usage — the surfaces a user reads at the moment they need the grammar.
func TestHelpTextUsesV2UrnGrammar(t *testing.T) {
	walkCommands(NewRootCmd(cmdutil.NewFactory()), func(cmd *cobra.Command) {
		for label, text := range map[string]string{
			"Short":   cmd.Short,
			"Long":    cmd.Long,
			"Example": cmd.Example,
		} {
			for _, bad := range v1Offenders(text) {
				t.Errorf("%s %s advertises v1 URN grammar; use hrn:mem:<root>:<slug> / hrn:node:<root>:<slug>:<loc>:\n\t%s",
					cmd.CommandPath(), label, bad)
			}
		}
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			for _, bad := range v1Offenders(f.Usage) {
				t.Errorf("%s --%s usage advertises v1 URN grammar:\n\t%s", cmd.CommandPath(), f.Name, bad)
			}
		})
	})
}

// The embedded agent contract is the highest-traffic doc of all, and agents
// copy from it verbatim.
func TestAgenticUsageUsesV2UrnGrammar(t *testing.T) {
	for _, bad := range v1Offenders(agentic.Doc()) {
		t.Errorf("agentic-usage.md advertises v1 URN grammar:\n\t%s", bad)
	}
}

// Shipped docs — same rule, same reason. docs/plans/ is deliberately excluded:
// those are design-as-built records of what was true when written, not
// instructions to follow.
func TestShippedDocsUseV2UrnGrammar(t *testing.T) {
	const repoRoot = "../.."
	files := []string{filepath.Join(repoRoot, "README.md")}
	for _, root := range []string{
		filepath.Join(repoRoot, "plugins"),
		filepath.Join(repoRoot, "docs", "how-to"),
	} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".md") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	if len(files) < 2 {
		t.Fatal("found almost no docs to check — the paths are wrong")
	}
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			if v1Grammar.MatchString(line) && !marksAsNonCanonical.MatchString(line) {
				t.Errorf("%s:%d advertises v1 URN grammar:\n\t%s", path, i+1, strings.TrimSpace(line))
			}
		}
	}
}
