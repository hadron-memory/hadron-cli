package cmd

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// `hadron agentic-usage` is the contract agents read INSTEAD of `--help`, and
// nothing made its condensed synopsis block agree with the binary. It drifted
// exactly as you would expect: hadron-cli#496 removed `--team-agent` from
// `worker cast`, the prose section a few hundred lines down said so, and the
// one-line quick reference at the top still advertised it — caught in review
// of PR #500, after I had swept the same file for the same removal.
//
// An agent copying the synopsis gets exit 2 for a flag the same document told
// it to pass, which is worse than having no quick reference at all. So every
// flag the synopsis names is checked against the command it names it for.
//
// Scoped to the `hadron team …` lines: the mechanism generalises, but that is
// where the churn is, and a check that flags prose is a check someone deletes.
func TestAgenticUsageSynopsisNamesOnlyRealFlags(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)

	flagRe := regexp.MustCompile(`--[a-z][a-z0-9-]*`)
	// Parenthesised asides are prose ("(uses --app or the binding)"), not spec.
	proseRe := regexp.MustCompile(`\([^)]*\buses\b[^)]*\)`)
	// `|` separates subcommands — EXCEPT inside a parenthesised alternation
	// like `(--role <role> | --agent <ref>)`. Splitting on it naively cut that
	// spec in half and left the tail starting with a flag, which the loop then
	// skipped as "not a subcommand" — silently dropping every flag after it,
	// including the `--team-agent` this test exists to catch. Found by
	// mutation-checking the test against the bug it was written for.
	altRe := regexp.MustCompile(`\([^()]*\)`)

	checked := 0
	for _, line := range strings.Split(string(readContract(t)), "\n") {
		if !strings.HasPrefix(line, "hadron team ") {
			continue
		}
		line = proseRe.ReplaceAllString(line, "")
		line = altRe.ReplaceAllStringFunc(line, func(g string) string {
			return strings.ReplaceAll(g, "|", "\x00")
		})
		words := strings.Fields(line)[1:] // drop "hadron"

		// `hadron team worker cast <spec> | list <spec> | …`: the words before
		// the first spec token are the path, and its LAST word is the first
		// subcommand — segments after `|` supply their own.
		lead := 0
		for lead < len(words) && !isSpecToken(words[lead]) {
			lead++
		}
		if lead == 0 {
			continue
		}
		prefix, rest := words[:lead-1], strings.Join(words[lead-1:], " ")

		for _, seg := range strings.Split(rest, "|") {
			segWords := strings.Fields(seg)
			if len(segWords) == 0 || isSpecToken(segWords[0]) {
				continue
			}
			path := append(append([]string{}, prefix...), segWords[0])
			cmd, _, err := root.Find(path)
			// cobra's Find walks as far as it can and returns the DEEPEST
			// match rather than failing, so an unmodelled shape resolves to a
			// parent and every check below would run against the wrong
			// command. Verify we landed on the leaf we asked for.
			if err != nil || cmd == nil || cmd.Name() != path[len(path)-1] {
				continue
			}
			checked++
			seg = strings.ReplaceAll(seg, "\x00", "|")
			for _, tok := range flagRe.FindAllString(seg, -1) {
				name := strings.TrimPrefix(tok, "--")
				if cmd.Flags().Lookup(name) == nil && root.PersistentFlags().Lookup(name) == nil {
					t.Errorf("`hadron %s` synopsis names --%s, which the command does not have:\n  %s",
						strings.Join(path, " "), name, strings.TrimSpace(seg))
				}
			}
		}
	}
	// The parser skips shapes it cannot model, so a reformatting of the block
	// would otherwise leave this test green and checking nothing.
	if checked < 10 {
		t.Errorf("only %d team subcommands matched — the synopsis parser has stopped working", checked)
	}
}

// The other half of the same drift, and not reachable through cobra: `worker
// cast --name` is required, but enforced in RunE rather than by
// MarkFlagRequired, so that the refusal can name the remedy instead of cobra's
// generic message (hadron-cli#496). Nothing structural stops the synopsis from
// bracketing it as optional again, so pin it.
func TestAgenticUsageShowsCastNameAsRequired(t *testing.T) {
	var line string
	for _, l := range strings.Split(string(readContract(t)), "\n") {
		if strings.HasPrefix(l, "hadron team worker cast ") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("the worker cast synopsis line is gone — this test is checking nothing")
	}
	cast := strings.SplitN(line, "|", 2)[0]
	if strings.Contains(cast, "[--name") {
		t.Errorf("--name is required; bracketing it reads as \"you may omit this\" and exits 2 when you do:\n  %s", cast)
	}
	if !strings.Contains(cast, "--name <n>") {
		t.Errorf("the synopsis must show --name:\n  %s", cast)
	}
}

func readContract(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatalf("read the embedded contract: %v", err)
	}
	return data
}

// isSpecToken reports whether a synopsis word describes ARGUMENTS rather than
// naming a subcommand: flags, placeholders, bracketed groups, alternations.
func isSpecToken(w string) bool {
	return strings.HasPrefix(w, "-") || strings.HasPrefix(w, "[") ||
		strings.HasPrefix(w, "<") || strings.HasPrefix(w, "(")
}
