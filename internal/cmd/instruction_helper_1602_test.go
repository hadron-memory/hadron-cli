package cmd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/app"
)

func TestInstructionMinimumHelp1602(t *testing.T) {
	for _, tc := range []struct {
		name            string
		path            []string
		minimum, absent string
	}{
		{"S1_update", []string{"team", "worker", "update"}, "17ef5905", "c3ca05b5"},
		{"S1_change", []string{"team", "worker", "change-agent"}, "17ef5905", "c3ca05b5"},
		{"S3_keys", []string{"app", "key", "list"}, "c3ca05b5", "17ef5905"},
		{"S3_revoke", []string{"app", "key", "revoke"}, "c3ca05b5", "17ef5905"},
		{"S3_scope", []string{"app", "key", "scope", "revoke"}, "c3ca05b5", "17ef5905"},
		{"S3_grants", []string{"app", "memory-grant", "list"}, "c3ca05b5", "17ef5905"},
		{"S3_rm", []string{"app", "memory-grant", "rm"}, "c3ca05b5", "17ef5905"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			c, _, err := root.Find(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			flag := c.Flags().Lookup("instruction")
			if flag == nil || !strings.Contains(flag.Usage, "Needs server "+tc.minimum+" or later") || strings.Contains(flag.Usage, tc.absent) {
				t.Fatalf("flag usage = %+v", flag)
			}
			if !strings.Contains(c.Long, "server "+tc.minimum+" or later") || strings.Contains(c.Long, tc.absent) {
				t.Fatalf("long help = %s", c.Long)
			}
		})
	}
}

func TestInstructedGrantIncludeRevoked1602(t *testing.T) {
	for _, tc := range []struct {
		name    string
		flags   []string
		value   any
		present bool
	}{
		{"omitted", nil, nil, false}, {"true", []string{"--include-revoked"}, true, true}, {"false", []string{"--include-revoked=false"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"app", "memory-grant", "list", "acme.com:team", "--instruction", "task:" + taskRef1592, "--json"}
			args = append(args, tc.flags...)
			calls, r := (run1592{bound: true, response: map[string]string{"AppMemoryWriteGrantsInstructed": `{"data":{"appMemoryWriteGrants":[]}}`}}).exec(t, args...)
			if r.err != nil || len(*calls) != 1 {
				t.Fatalf("error=%v calls=%+v", r.err, *calls)
			}
			c := (*calls)[0]
			v, present := c.Vars["includeRevoked"]
			if c.Op != "AppMemoryWriteGrantsInstructed" || c.Session != "s-new" || present != tc.present || !reflect.DeepEqual(v, tc.value) || !reflect.DeepEqual(c.Vars["instruction"], map[string]any{"kind": "TASK", "ref": taskRef1592}) {
				t.Fatalf("wire=%+v", c)
			}
		})
	}
}

func TestCredentialNilInstructionHook1602(t *testing.T) {
	for _, tc := range credentialCases1595() {
		t.Run(tc.op, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{tc.op + "Instructed": `{"data":` + tc.body + `}`})
			f, _ := testFactory(t)
			f.ServerFlag = srv.URL
			t.Setenv("HADRON_TOKEN", "hdr_user_test")
			// Deliberately construct the App group with a bare factory, without root's hook.
			c := app.NewCmdApp(f)
			c.SetArgs(append(append([]string{}, tc.args[1:]...), "--instruction", "live-chat:client:7"))
			err := c.Execute()
			if exitCodeFor(err) != 2 || len(*calls) != 0 {
				t.Fatalf("nil hook error=%v calls=%+v", err, *calls)
			}
		})
	}
}

func TestCredentialInstructionRefBytes1602(t *testing.T) {
	for _, tc := range credentialCases1595() {
		t.Run(tc.op, func(t *testing.T) {
			ref := " client:7 \t"
			op := tc.op + "Instructed"
			calls, r := (run1592{bound: true, response: map[string]string{op: `{"data":` + tc.body + `}`}}).exec(t, append(append([]string{}, tc.args...), "--instruction", "live-chat:"+ref, "--json")...)
			if r.err != nil || len(*calls) != 1 {
				t.Fatalf("error=%v calls=%+v", r.err, *calls)
			}
			c := (*calls)[0]
			if c.Op != op || c.Session != "s-new" || !reflect.DeepEqual(c.Vars["instruction"], map[string]any{"kind": "LIVE_CHAT", "ref": ref}) {
				t.Fatalf("ref bytes lost: %+v", c)
			}
		})
	}
}
