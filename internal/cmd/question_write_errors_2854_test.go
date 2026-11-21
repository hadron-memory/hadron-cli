package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2854: ask, amend, withdraw and purge map a failed write the way answer and retract do
// (cli#2680). A coded refusal AT the mutation field keeps its typed exit; a coded error BELOW
// the field, a null result or a failure after the request left is an unknown outcome (exit 7,
// "not confirmed", not resent) that names how to read the outcome back, worded per verb.
func TestQuestionOtherWritesMapErrorsBelowTheFieldAsUnknown2854(t *testing.T) {
	askFile := filepath.Join(t.TempDir(), "ask.json")
	if err := os.WriteFile(askFile, []byte(`{"title":"Pick a database"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	type verb struct {
		name, op, field string
		args            []string
		readBack        string // what the unknown-outcome sentence must tell the operator to run
		hasNull         bool   // a null mutation result is decodable as "no question"
	}
	verbs := []verb{
		{"ask", "AskQuestion", "askQuestion", []string{"ask", "LOG", "--input-file", askFile}, "hadron question list --log LOG", true},
		{"amend", "AmendQuestion", "amendQuestion", []string{"amend", "QA", "--expected-revision", "3", "--reason", "r", "--title", "T"}, "hadron question get QA", true},
		{"withdraw", "WithdrawQuestion", "withdrawQuestion", []string{"withdraw", "QA", "--expected-revision", "3", "--reason", "r", "--yes"}, "hadron question get QA", true},
		{"purge", "PurgeUnansweredQuestion", "purgeUnansweredQuestion", []string{"purge", "QA", "--reason", "r", "--yes"}, "hadron question get QA", false},
	}
	for _, v := range verbs {
		// what the field resolves to when the write ran: the question, or purge's true
		data := questionAnsweredJSON
		if v.name == "purge" {
			data = "true"
		}
		at := func(code, path string) string {
			return `{"data":{"` + v.field + `":null},"errors":[{"message":"x","path":` + path + `,"extensions":{"code":"` + code + `"}}]}`
		}
		cases := map[string]struct {
			resp string
			exit int
		}{
			"NOT_FOUND below the field":               {at("NOT_FOUND", `["`+v.field+`","answer"]`), exitcode.Unavailable},
			"FORBIDDEN below the field, beside data":  {`{"data":{"` + v.field + `":` + data + `},"errors":[{"message":"x","path":["` + v.field + `","answers",0,"actor"],"extensions":{"code":"FORBIDDEN"}}]}`, exitcode.Unavailable},
			"an internal error at the field":          {at("INTERNAL_SERVER_ERROR", `["`+v.field+`"]`), exitcode.Unavailable},
			"FORBIDDEN at the field keeps its exit":   {at("FORBIDDEN", `["`+v.field+`"]`), exitcode.Forbidden},
			"NOT_FOUND with no path keeps its exit":   {`{"errors":[{"message":"x","extensions":{"code":"NOT_FOUND"}}]}`, exitcode.NotFound},
			"a conflict at the field keeps its exit":  {at("NODE_WRITE_CONFLICT", `["`+v.field+`"]`), exitcode.Conflict},
			"a typed refusal at the field keeps exit": {at("QUESTION_WITHDRAWN", `["`+v.field+`"]`), -1},
		}
		if v.hasNull {
			cases["a null result with no error"] = struct {
				resp string
				exit int
			}{`{"data":{"` + v.field + `":null}}`, exitcode.Unavailable}
		}
		for name, c := range cases {
			calls, out, err := runQuestion(t, map[string]string{v.op: c.resp}, v.args...)
			if err == nil || len(calls.ops) != 1 {
				t.Errorf("%s %s: want one request and an error, got sends=%d err=%v", v.name, name, len(calls.ops), err)
				continue
			}
			if c.exit == -1 {
				// a typed refusal at the field: whatever its exit, it is never an unknown outcome
				if strings.Contains(err.Error(), "not confirmed") || exitCodeFor(err) == exitcode.Unavailable {
					t.Errorf("%s %s: a refusal at the field must keep its typed exit, got exit=%d %v", v.name, name, exitCodeFor(err), err)
				}
				continue
			}
			if exitCodeFor(err) != c.exit {
				t.Errorf("%s %s: exit=%d want=%d err=%v", v.name, name, exitCodeFor(err), c.exit, err)
				continue
			}
			if c.exit != exitcode.Unavailable {
				if strings.Contains(err.Error(), "not confirmed") {
					t.Errorf("%s %s: a refusal must not claim an unknown outcome: %v", v.name, name, err)
				}
				continue
			}
			m := err.Error()
			if !strings.Contains(m, "not confirmed") || !strings.Contains(m, "not resent") || !strings.Contains(m, v.readBack) || out != "" {
				t.Errorf("%s %s: an unknown outcome says so, is not resent and names %q: %v out=%q", v.name, name, v.readBack, err, out)
			}
			if v.name == "purge" && !strings.Contains(m, "not found") {
				t.Errorf("purge %s: the read-back must say a purged question reads as not found: %v", name, err)
			}
		}
	}
}
