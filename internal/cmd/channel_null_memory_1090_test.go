package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// A readable parent with withheld memory metadata is still a successful row.
// Inspect raw keys: decoding a missing key to *string cannot prove explicit null.
func TestChannelNullMemory1090(t *testing.T) {
	nullRow := strings.Replace(channelJSON, `"memory":{"id":"m1","urn":"hrn:mem:acme.com:team-shared","name":"Team shared"}`, `"memory":null`, 1)
	cases := []struct {
		name, op, field string
		args            []string
	}{
		{"get", "GetChannel", "channel", []string{"get", "room"}},
		{"create", "CreateChannel", "createChannel", []string{"create", "room", "-m", "hrn:mem:acme.com:team-shared", "--loc", "chats:room"}},
		{"update", "UpdateChannel", "updateChannel", []string{"update", "room", "--name", "new"}},
		{"list", "Channels", "channels", []string{"list"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, asJSON := range []bool{true, false} {
				data := nullRow
				if tc.name == "list" {
					data = `{"total":3,"items":[` + channelJSON + `,` + nullRow + `,` + channelNoAddressJSON + `]}`
				}
				gql, calls := captureGraphQL(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":` + data + `}}`, "NodePortal": `{"data":{"node":null}}`})
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := append([]string{"channel"}, tc.args...)
				args = append(args, "--server", gql.URL)
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatalf("readable parent refused: %v", err)
				}
				wantCalls := 1
				if tc.name == "get" {
					wantCalls = 2
				} // existing NodePortal enrichment only
				if len(calls) != wantCalls {
					t.Fatalf("unexpected inference queries: %v", calls)
				}
				if !asJSON {
					if !strings.Contains(out.String(), "not readable") {
						t.Errorf("missing fixed marker: %s", out.String())
					}
					continue
				}
				var row map[string]json.RawMessage
				if tc.name == "list" {
					var rows []map[string]json.RawMessage
					if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
						t.Fatal(err)
					}
					if len(rows) != 3 {
						t.Fatalf("lost siblings: %s", out.String())
					}
					row = rows[1]
					if string(rows[0]["memoryUrn"]) != `"hrn:mem:acme.com:team-shared"` || string(rows[2]["memoryName"]) != `"Legacy"` {
						t.Fatalf("changed siblings: %s", out.String())
					}
				} else if err := json.Unmarshal([]byte(out.String()), &row); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"memoryUrn", "memoryName"} {
					if value, ok := row[key]; !ok || string(value) != "null" {
						t.Errorf("%s must exist as null, got %s", key, row[key])
					}
				}
				if string(row["memoryId"]) != `"m1"` || string(row["lastSeq"]) != "42" {
					t.Errorf("parent changed: %s", out.String())
				}
			}
		})
	}
}

func TestChannelReadableEmptyMemory1090(t *testing.T) {
	row := strings.Replace(channelJSON, `"urn":"hrn:mem:acme.com:team-shared","name":"Team shared"`, `"urn":"","name":""`, 1)
	gql, _ := captureGraphQL(t, map[string]string{"GetChannel": `{"data":{"channel":` + row + `}}`, "NodePortal": `{"data":{"node":null}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "get", "room", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out.String()), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"memoryUrn", "memoryName"} {
		if string(raw[key]) != `""` {
			t.Errorf("readable empty %s changed: %s", key, out.String())
		}
	}
}
