package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestChatHeadsWire1833(t *testing.T) {
	for _, surface := range []string{"team", "channel"} {
		for _, heads := range [][]string{{" head-a "}, {"head-a", "head-b"}, {"a,b", "head-b"}} {
			t.Run(surface+"/"+strings.Join(heads, "-"), func(t *testing.T) {
				r := taskResponses()
				for _, op := range []string{"TeamChatTags", "TeamChatTagsAttributed", "TeamChatTagsMulti", "TeamChatTagsMultiAttributed"} {
					r[op] = `{"data":{"teamChatMessages":{"items":[]}}}`
				}
				for _, op := range []string{"ChannelTags", "ChannelTagsMulti"} {
					r[op] = `{"data":{"channelMessages":{"total":0,"items":[]}}}`
				}
				args := []string{"team", "chat", "read", "--json", "--limit", "5"}
				if surface == "channel" {
					args = []string{"channel", "read", "hrn:node:acme.com:kb:chat", "--json"}
				}
				for _, head := range heads {
					args = append(args, "--team-head", head)
				}
				calls, _, err := runTeamTask(t, r, args...)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, c := range *calls {
					if !strings.Contains(c.Op, "Tags") {
						continue
					}
					found = true
					if len(heads) == 1 {
						if c.Vars["teamHeadRef"] != heads[0] || strings.Contains(c.Query, "teamHeadRefs") {
							t.Fatalf("single-head wire changed: %+v", c)
						}
					} else {
						want := []any{}
						for _, h := range heads {
							want = append(want, h)
						}
						if !reflect.DeepEqual(c.Vars["teamHeadRefs"], want) {
							t.Fatalf("plural heads lost/order normalized: %+v", c)
						}
						if _, ok := c.Vars["teamHeadRef"]; ok {
							t.Fatalf("both head forms sent: %+v", c)
						}
						if strings.Contains(c.Query, "teamHeadRef:") {
							t.Fatalf("singular argument on plural document: %s", c.Query)
						}
					}
				}
				if !found {
					t.Fatalf("no tag read: %+v", calls)
				}
			})
		}
	}
}

func TestDueWakesPreview1833(t *testing.T) {
	for _, preview := range []bool{false, true} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(map[bool]string{false: "normal", true: "preview"}[preview]+map[bool]string{false: "/text", true: "/json"}[jsonMode], func(t *testing.T) {
				r := c1279Responses(c1279Page("OK", 0, c1279TickWake))
				r["TeamDueWakesPreview"] = r["TeamDueWakes"]
				args := []string{"team", "wakes", "due", "--accept-stop"}
				if preview {
					args = append(args, "--preview")
				}
				if jsonMode {
					args = append(args, "--json")
				}
				calls, out, err := runTeamTask(t, r, args...)
				if err != nil {
					t.Fatal(err)
				}
				reads := 0
				for _, c := range *calls {
					if !strings.HasPrefix(c.Op, "TeamDueWakes") {
						continue
					}
					reads++
					if c.Session != "" {
						t.Fatalf("wake read attributed: %+v", c)
					}
					if strings.Contains(c.Query, "preview: true") != preview {
						t.Fatalf("wrong preview document: %s", c.Query)
					}
				}
				if reads != 1 {
					t.Fatalf("unexpected reads: %+v", calls)
				}
				if jsonMode {
					var got map[string]any
					if err := json.Unmarshal([]byte(out), &got); err != nil {
						t.Fatal(err)
					}
					if preview {
						if got["preview"] != true {
							t.Fatalf("preview not labelled: %s", out)
						}
						if got["wakes"].([]any)[0].(map[string]any)["proof"] != "" {
							t.Fatalf("preview exposes ack-able proof: %s", out)
						}
					} else if _, ok := got["preview"]; ok {
						t.Fatalf("default JSON shape changed: %s", out)
					}
				} else if preview {
					if !strings.Contains(out, "preview") || strings.Contains(out, "prf-tick-1") || strings.Contains(out, "proof:") {
						t.Fatalf("preview not clearly unack-able: %s", out)
					}
				} else if !strings.Contains(out, "prf-tick-1") {
					t.Fatalf("ordinary proof lost: %s", out)
				}
			})
		}
	}
}
