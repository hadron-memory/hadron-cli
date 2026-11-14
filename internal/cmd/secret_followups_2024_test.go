package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

// cli#2024 (follow-ups from #2022): empty-page wording and refusal-code pins.

const emptyPageNote2024 = "(no rows on this page; --limit 0 counts only, and --offset may be past the end)"

// Item 1: an empty page with a positive total names both causes, for both
// commands; a real zero total and a non-empty page are unchanged.
func TestSecretEmptyPageWording2024(t *testing.T) {
	versions := func(total int, items string) string {
		return `{"data":{"secret":{"id":"s","name":"n","currentVersion":3,"protected":false,"versions":{"total":` +
			itoa(total) + `,"hasMore":false,"items":[` + items + `]}}}}`
	}
	events := func(total int, items string) string {
		return `{"data":{"secretEvents":{"total":` + itoa(total) + `,"items":[` + items + `]}}}`
	}
	const vrow = `{"versionNo":3,"sizeBytes":1,"writtenAt":"2026-11-14T03:00:00Z","supersededAt":null,"writtenByUserId":null,"writtenByWorkerId":null}`
	const erow = `{"id":"e1","at":"2026-11-14T03:00:00Z","kind":"VERSION","outcome":"OK","secretId":"s","secretName":"n","versionNo":3,"actorUserId":null,"workerId":null,"sessionId":null}`
	for _, c := range []struct {
		name, op, cmd, prefix, none, row string
		resp                             func(int, string) string
	}{
		{"versions", "SecretVersions", "versions", "kept versions", "no versions", "3", versions},
		{"events", "SecretEvents", "events", "events", "no events", "VERSION", events},
	} {
		items := vrow
		if c.name == "events" {
			items = erow
		}
		t.Run(c.name, func(t *testing.T) {
			for _, paging := range [][]string{{"--limit", "0"}, {"--limit", "5", "--offset", "9"}} {
				out, cap, err := runSecret(t, "", map[string]string{c.op: c.resp(4, "")}, append([]string{c.cmd, "s"}, paging...)...)
				if err != nil || !strings.Contains(out, "4 "+c.prefix+" "+emptyPageNote2024) || strings.Contains(out, c.none) {
					t.Fatalf("%v %v %q", paging, err, out)
				}
				var vars struct{ Limit, Offset *int }
				_ = json.Unmarshal(cap[c.op], &vars)
				if vars.Limit == nil || *vars.Limit != mustAtoi2024(paging[1]) || (len(paging) > 2) != (vars.Offset != nil) {
					t.Fatalf("paging not forwarded: %s", cap[c.op])
				}
				j, _, err := runSecret(t, "", map[string]string{c.op: c.resp(4, "")}, append([]string{c.cmd, "s", "--json"}, paging...)...)
				if err != nil || strings.Contains(j, "no rows") || (!strings.Contains(j, `"`+c.cmd+`": []`) && !strings.Contains(j, `"`+c.cmd+`":[]`)) || !strings.Contains(j, `"total": 4`) {
					t.Fatalf("JSON must stay unchanged: %v %s", err, j)
				}
			}
			out, _, err := runSecret(t, "", map[string]string{c.op: c.resp(0, "")}, c.cmd, "s")
			if err != nil || !strings.Contains(out, c.none) || strings.Contains(out, "no rows") {
				t.Fatalf("zero total: %v %q", err, out)
			}
			out, _, err = runSecret(t, "", map[string]string{c.op: c.resp(1, items)}, c.cmd, "s")
			if err != nil || !strings.Contains(out, c.row) || strings.Contains(out, "no rows") {
				t.Fatalf("non-empty page: %v %q", err, out)
			}
		})
	}
}

// Item 4: create/get/list pass the server's refusal code through unchanged.
func TestSecretCreateGetListRefusalCodes2024(t *testing.T) {
	refuse := func(ext string) string {
		return `{"errors":[{"message":"refused","extensions":` + ext + `}]}`
	}
	cases := []struct {
		name, op, stdin string
		args            []string
	}{
		{"create", "CreateSecret", "tok-material\n", []string{"secret", "create", "--name", "n", "--scope", "app", "--owner", "hrn:app:acme.com:ops", "--kind", "generic", "--value-file", "-", "--json"}},
		{"get", "Secret", "", []string{"secret", "get", "s", "--json"}},
		{"list", "Secrets", "", []string{"secret", "list", "--scope", "app", "--owner", "hrn:app:acme.com:ops", "--json"}},
	}
	for _, c := range cases {
		for _, k := range []struct{ name, ext, want string }{
			{"protected", `{"code":"SECRET_PROTECTED"}`, "SECRET_PROTECTED"},
			{"unknown", `{"code":"SYNTHETIC_UNKNOWN_2024"}`, "SYNTHETIC_UNKNOWN_2024"},
			{"none", `{}`, ""},
		} {
			t.Run(c.name+"/"+k.name, func(t *testing.T) {
				gql, cap := captureGraphQL(t, map[string]string{c.op: refuse(k.ext)})
				f, out := testFactory(t)
				f.IOStreams.In = strings.NewReader(c.stdin)
				var errOut bytes.Buffer
				f.IOStreams.ErrOut = &errOut
				args := append(append([]string{}, c.args...), "--server", gql.URL)
				root := NewRootCmd(f)
				root.SetArgs(args)
				err := root.Execute()
				if err == nil {
					t.Fatal("expected a refusal")
				}
				got := ""
				var coded interface{ ServerCode() string }
				if errors.As(err, &coded) {
					got = coded.ServerCode()
				}
				if got != k.want {
					t.Fatalf("ServerCode %q, want %q", got, k.want)
				}
				exit := renderFailure(f, args, err)
				text := out.String() + errOut.String()
				if k.want == "" {
					if strings.Contains(text, "serverCode") || strings.Contains(text, "SECRET_PROTECTED") {
						t.Fatalf("manufactured a code: %s", text)
					}
				} else if !strings.Contains(text, `"serverCode": "`+k.want+`"`) {
					t.Fatalf("envelope lacks serverCode %s: %s", k.want, text)
				}
				if exit != exitCodeFor(err) || exit == 0 {
					t.Fatalf("exit %d vs %d", exit, exitCodeFor(err))
				}
				if len(cap) != 1 {
					t.Fatalf("expected exactly one operation, got %d", len(cap))
				}
				if strings.Contains(text, "tok-material") {
					t.Fatalf("secret material in error output: %s", text)
				}
			})
		}
	}
}

func mustAtoi2024(s string) int { n, _ := strconv.Atoi(s); return n }
