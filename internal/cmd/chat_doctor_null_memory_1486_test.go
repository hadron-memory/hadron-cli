package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatDoctorNullMemory1486(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[asJSON], func(t *testing.T) {
			chatConfigDir(t, `{"chat":{"node":"hrn:node:acme.com:tc:chats:api:messages"}}`)
			channel := strings.Replace(doctorChannelJSON, `"memory":{"id":"m1","urn":"hrn:mem:acme.com:tc","name":"tc"}`, `"memory":null`, 1)
			if channel == doctorChannelJSON {
				t.Fatal("fixture replacement missed")
			}
			gql, calls := captureGraphQL(t, map[string]string{"GetChannel": `{"data":{"channel":` + channel + `}}`})
			args := []string{"chat", "doctor", "--server", gql.URL}
			if asJSON {
				args = append(args, "--json")
			}
			out, err := runChat(t, args...)
			if err != nil {
				t.Fatalf("doctor: %v (%s)", err, out)
			}
			if asJSON {
				var dto map[string]any
				if err := json.Unmarshal([]byte(out), &dto); err != nil {
					t.Fatal(err)
				}
				ch := dto["channel"].(map[string]any)
				if dto["ok"] != true || ch["status"] != "readable" || ch["id"] != "0123456789abcdef0123456789abcdef" {
					t.Fatal(dto)
				}
				if mem, present := ch["memory"]; !present || mem != nil {
					t.Fatalf("memory=%v present=%v", mem, present)
				}
			} else if !strings.Contains(out, "channel: readable") || !strings.Contains(out, "memory: unavailable") || !strings.Contains(out, "ok: config parsed") {
				t.Fatal(out)
			}
			for op := range calls {
				if op != "GetChannel" {
					t.Fatalf("unexpected operation %s", op)
				}
			}
		})
	}
}

func TestChatDoctorUnavailableWording1486(t *testing.T) {
	chatConfigDir(t, `{"chat":{"node":"hrn:node:acme.com:tc:chats:api:messages"}}`)
	gql, _ := captureGraphQL(t, map[string]string{"GetChannel": `{"data":{"channel":null}}`})
	out, err := runChat(t, "chat", "doctor", "--server", gql.URL)
	if exitCodeFor(err) != 4 || !strings.Contains(out, "no Channel is readable") || strings.Contains(out, "read its host memory") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestChatDoctorReadableMemory1486(t *testing.T) {
	chatConfigDir(t, `{"chat":{"node":"hrn:node:acme.com:tc:chats:api:messages"}}`)
	gql, _ := captureGraphQL(t, map[string]string{"GetChannel": `{"data":{"channel":` + doctorChannelJSON + `}}`})
	out, err := runChat(t, "chat", "doctor", "--json", "--server", gql.URL)
	var dto map[string]any
	if err != nil || json.Unmarshal([]byte(out), &dto) != nil {
		t.Fatalf("%q %v", out, err)
	}
	ch := dto["channel"].(map[string]any)
	if dto["ok"] != true || ch["status"] != "readable" || ch["memory"] != "hrn:mem:acme.com:tc" {
		t.Fatal(dto)
	}
	out, err = runChat(t, "chat", "doctor", "--server", gql.URL)
	if err != nil || !strings.Contains(out, "channel: readable") || strings.Contains(out, "memory: unavailable") {
		t.Fatalf("%q %v", out, err)
	}
}
