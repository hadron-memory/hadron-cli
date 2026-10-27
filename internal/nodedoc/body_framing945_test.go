package nodedoc

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Server-owned fixture v1, copied byte-for-byte from hadron-server#2585 at
// 0aab43f5a45e1fb5b79ed6c3b380d8c9925ec85a. Keep copies synchronized.
func TestMarkdownBodyFraming945(t *testing.T) {
	var fixture struct {
		Version     string `json:"version"`
		Frontmatter string `json:"frontmatter"`
		Cases       []struct {
			Name, Kind     string
			Stored, Loaded *string
			Body           string
		}
	}
	raw, err := os.ReadFile("testdata/markdownBodyFraming.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Version != "markdown-body-framing-v1" {
		t.Fatalf("unknown fixture %q", fixture.Version)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			want := ""
			if c.Loaded != nil {
				want = *c.Loaded
			}
			got, err := ParseMarkdown([]byte(fixture.Frontmatter + c.Body))
			if err != nil {
				t.Fatal(err)
			}
			if got.Content != want {
				t.Errorf("parsed %q, want %q", got.Content, want)
			}
			if c.Kind != "parse" {
				stored := ""
				if c.Stored != nil {
					stored = *c.Stored
				}
				rendered, err := RenderMarkdown(&Document{Name: "fixture", Content: stored}, false)
				if err != nil {
					t.Fatal(err)
				}
				parts := strings.SplitN(rendered, "\n---\n", 2)
				if len(parts) != 2 || parts[1] != c.Body {
					t.Fatalf("writer framing drift: %q", rendered)
				}
				round, err := ParseMarkdown([]byte(rendered))
				if err != nil {
					t.Fatal(err)
				}
				if round.Content != want {
					t.Errorf("round-trip %q, want %q", round.Content, want)
				}
				if c.Kind == "roundtrip" && ContentHash(round.Content) != ContentHash(stored) {
					t.Error("preserved body changed hash")
				}
			}
		})
	}
}

func TestMarkdownInputHashDiscarded945(t *testing.T) {
	d, err := ParseMarkdown([]byte("---\nname: X\ncontentHash: stale-input\n---\n\n    code\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d.ContentHash != "" {
		t.Fatalf("input hash retained: %q", d.ContentHash)
	}
	rendered, err := RenderMarkdown(d, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "contentHash: "+ContentHash(d.Content)) {
		t.Fatal("export must recompute body hash")
	}
}

func TestJSONWhitespaceUnchanged945(t *testing.T) {
	for _, body := range []string{"", " \t\n\r\v\f", "    code\n", "A\r\nB\r", "\u00a0\ufeff\u0085\u2028"} {
		rendered, err := RenderJSON(&Document{Content: body})
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseJSON([]byte(rendered))
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Content != body {
			t.Errorf("JSON changed %q to %q", body, parsed.Content)
		}
	}
}
