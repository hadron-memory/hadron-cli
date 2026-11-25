package cmd

import (
	"strings"
	"testing"
)

// cli#3146: the chatSeenSeq naming sweep. The binding's key is a per-worktree RENDERING CACHE
// (cli#2906); `chat read` and `chat mark-read` help, and the agent contract's mark-read paragraph,
// still called it the "local watermark" that `session log` counts from, which has been false since
// #2858. These pins keep every place a reader meets the cache on one name, say who writes and who
// consults it, and state the start condition in terms of the flag.

// staleChatSeenSeqNames are phrases that name the cache wrongly or say session log reads it. They
// are matched on whitespace-flattened text.
var staleChatSeenSeqNames3146 = []string{
	"local watermark",
	"local read watermark",
	"local read hint",
	"`team session log` counts from",
	"`hadron team session log` compares against",
	"the watermark `session log` compares against",
	"holds two hints",
}

func chatSeenSeqHelps3146(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"team chat read --help":      flat(flatHelp2565(t, "team", "chat", "read")),
		"team chat mark-read --help": flat(flatHelp2565(t, "team", "chat", "mark-read")),
		"team session whoami --help": flat(flatHelp2565(t, "team", "session", "whoami")),
	}
}

// Both chat commands call the key the rendering cache, in the same words as whoami.
func TestChatHelpsCallChatSeenSeqTheRenderingCache3146(t *testing.T) {
	helps := chatSeenSeqHelps3146(t)
	for _, name := range []string{"team chat read --help", "team chat mark-read --help"} {
		if !strings.Contains(helps[name], "rendering cache") {
			t.Errorf("%s must call the local record the rendering cache:\n%s", name, helps[name])
		}
	}
}

// The stale list runs over all three helps and over the contract.
func TestChatSeenSeqStaleNamesAreGoneEverywhere3146(t *testing.T) {
	texts := chatSeenSeqHelps3146(t)
	texts["agentic-usage"] = flat(string(readContract(t)))
	for name, text := range texts {
		for _, stale := range staleChatSeenSeqNames3146 {
			if strings.Contains(text, stale) {
				t.Errorf("%s still says %q", name, stale)
			}
		}
	}
}

// The contract's mark-read paragraph uses the same name and the real effect.
func TestAgenticUsageMarkReadParagraphNamesTheRenderingCache3146(t *testing.T) {
	contract := flat(string(readContract(t)))
	for _, want := range []string{
		// Writers: `chat mark-read` on the team chat also writes the cache.
		"a successful mark also moves the binding's rendering cache",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("agentic-usage must say %q", want)
		}
	}
}

// Only `chat read` consults the cache; `chat mark-read` on the team chat also writes it. Both
// the whoami help and the contract say both halves.
func TestChatSeenSeqWritersAreNamed3146(t *testing.T) {
	whoami := chatSeenSeqHelps3146(t)["team session whoami --help"]
	contract := flat(string(readContract(t)))
	for name, text := range map[string]string{"whoami help": whoami, "agentic-usage": contract} {
		for _, want := range []string{
			"only `chat read` consults it",
			"`chat mark-read` on the team chat also writes it",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s must say %q", name, want)
			}
		}
	}
}

// The start condition is stated in terms of the flag, and the effect is named (the cache advances
// and the server-side read mark is sent) rather than "counts". It is not presented as sufficient.
func TestChatSeenSeqBoundaryAndEffectAreStated3146(t *testing.T) {
	whoami := chatSeenSeqHelps3146(t)["team session whoami --help"]
	contract := flat(string(readContract(t)))
	for name, text := range map[string]string{"whoami help": whoami, "agentic-usage": contract} {
		for _, want := range []string{
			"A read can only advance it when `--since` is at or below the cache, or is `0` when there is none.",
			"Then the cache advances and the server-side read mark is sent.",
			"That is necessary, not sufficient: a read with no cursor, `--before`, a filter or `--thread` never qualifies.",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s must say %q", name, want)
			}
		}
	}
}
