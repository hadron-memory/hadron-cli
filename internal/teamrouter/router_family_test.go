package teamrouter

// Ported 1:1 from router.test.ts describe('screenAgentFamily — positive
// family chrome only').

import "testing"

func TestScreenFamilyClaudeVariants(t *testing.T) {
	for name, tail := range map[string][]string{
		"idle":      claudeIdle,
		"fresh":     claudeFresh,
		"compacted": claudeIdleCompacted,
		"real-xan":  claudeIdleRealXan,
		"real-liam": claudeIdleRealLiam,
		"busy":      claudeBusyEmpty,
		"quiet":     claudeUnknownQuiet,
	} {
		if got := ScreenAgentFamily(&Screen{Source: "screen", Tail: tail}); got != "claude" {
			t.Errorf("%s screen read %q, want claude", name, got)
		}
	}
}

func TestScreenFamilyCodexVariants(t *testing.T) {
	for name, tail := range map[string][]string{
		"idle":  codexIdle,
		"fresh": codexFresh,
		"busy":  codexBusyEmpty,
	} {
		if got := ScreenAgentFamily(&Screen{Source: "screen", Tail: tail}); got != "codex" {
			t.Errorf("%s screen read %q, want codex", name, got)
		}
	}
}

func TestScreenFamilyPlainShellReadsNull(t *testing.T) {
	cases := map[string]*Screen{
		"shell prompt":  {Source: "screen", Tail: []string{"orca@orca1:~/workspaces/eli$ ", ""}},
		"starship only": {Source: "screen", Tail: []string{"❯ "}}, // starship-like glyph alone is not claude
		"one rule":      {Source: "screen", Tail: []string{rule}}, // one rule is not the composer box
		"nil screen":    nil,                                      // unreadable terminal
		"stream source": {Source: "stream", Tail: []string{"x"}},  // not a screen
		// Ordinary output MENTIONING the markers is not chrome: banner/completion
		// markers anchor to banner-chrome line starts, never after arbitrary
		// content, and a bare › glyph is a shell theme as easily as a composer
		// (blind review P2, #1823).
		"echo codex marker":    {Source: "screen", Tail: []string{"orca$ echo \"Worked for 33s\"", "orca$ "}},
		"echo claude marker":   {Source: "screen", Tail: []string{"orca$ echo \"Claude Code v2.1.296\"", "orca$ "}},
		"echo composer prompt": {Source: "screen", Tail: []string{"orca$ echo \"Ask Codex to do anything\"", "orca$ "}},
		"bare glyph":           {Source: "screen", Tail: []string{"› ", ""}},
	}
	for name, sc := range cases {
		if got := ScreenAgentFamily(sc); got != "" {
			t.Errorf("%s read %q, want unresolved", name, got)
		}
	}
}
