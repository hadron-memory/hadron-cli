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

func TestChatHeadsValidationAndRefusals1833(t *testing.T) {
	for _, extra := range [][]string{
		{"--team-head", "a", "--team-head", " ", "--json"},
		{"--team-head", "a", "--team-head", "b", "--tagged"},
		{"--team-head", "a", "--team-head", "b", "--untagged"},
		{"--team-head", "a", "--team-head", "b", "--thread", "1"},
	} {
		calls, _, err := runTeamTask(t, taskResponses(), append([]string{"team", "chat", "read"}, extra...)...)
		if exitCodeFor(err) != 2 {
			t.Fatalf("conflict/blank not refused: %v", err)
		}
		if len(*calls) != 0 {
			t.Fatalf("invalid flags sent requests: %+v", *calls)
		}
	}
	// No local 20-head cap: the server owns it and its refusal remains visible.
	args := []string{"team", "chat", "read", "--json"}
	for i := 0; i < 21; i++ {
		args = append(args, "--team-head", jsonInt(i))
	}
	r := taskResponses()
	r["TeamChatTagsMultiAttributed"] = c1279Errors("BAD_USER_INPUT")
	calls, _, err := runTeamTask(t, r, args...)
	if exitCodeFor(err) != 2 {
		t.Fatalf("cap refusal mapping: %v", err)
	}
	found := stopCalls(calls, "TeamChatTagsMultiAttributed")
	if len(found) != 1 || len(found[0].Vars["teamHeadRefs"].([]any)) != 21 {
		t.Fatalf("cap handled locally or retried: %+v", *calls)
	}
}

func TestNewArgumentSkewNeverWidens1833(t *testing.T) {
	for _, kind := range []string{"heads", "preview"} {
		r := taskResponses()
		op := "TeamChatTagsMultiAttributed"
		args := []string{"team", "chat", "read", "--team-head", "a", "--team-head", "b", "--json"}
		message := "Unknown argument \"teamHeadRefs\" on field \"Query.teamChatMessages\"."
		if kind == "preview" {
			op = "TeamDueWakesPreview"
			args = []string{"team", "wakes", "due", "--preview"}
			message = "Unknown argument \"preview\" on field \"Query.dueWakes\"."
		}
		response, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}}})
		r[op] = string(response)
		calls, _, err := runTeamTask(t, r, args...)
		if exitCodeFor(err) != 2 {
			t.Fatalf("unknown argument should refuse: %v", err)
		}
		seen := 0
		for _, c := range *calls {
			if c.Op == op {
				seen++
			}
			if c.Op == "TeamDueWakes" || c.Op == "TeamChatTags" || c.Op == "TeamChatTagsAttributed" || c.Op == "TeamChatReadPage" {
				t.Fatalf("requested capability widened on skew: %+v", c)
			}
		}
		if seen != 1 {
			t.Fatalf("capability request retried: %+v", *calls)
		}
	}
}

func TestPluralHeadsAttributionFallback1833(t *testing.T) {
	r := taskResponses()
	r["TeamChatTagsMultiAttributed"] = `{"errors":[{"message":"Unknown argument \"advanceReadState\" on field \"Query.teamChatMessages\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	r["TeamChatTagsMulti"] = `{"data":{"teamChatMessages":{"items":[]}}}`
	calls, _, err := runTeamTask(t, r, "team", "chat", "read", "--team-head", "a", "--team-head", "b", "--json")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range *calls {
		if !strings.Contains(c.Op, "Tags") {
			continue
		}
		n++
		if !reflect.DeepEqual(c.Vars["teamHeadRefs"], []any{"a", "b"}) {
			t.Fatalf("fallback lost plural filter: %+v", c)
		}
		if c.Op == "TeamChatTagsMulti" && c.Session != "" {
			t.Fatalf("fallback not headerless: %+v", c)
		}
	}
	if n != 2 {
		t.Fatalf("want attributed and headerless plural attempts: %+v", *calls)
	}
}
