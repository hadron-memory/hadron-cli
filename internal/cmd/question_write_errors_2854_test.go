package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
		{"ask", "AskQuestion", "askQuestion", []string{"ask", "LOG", "--input-file", askFile}, "hadron question list --log <log>", true},
		{"amend", "AmendQuestion", "amendQuestion", []string{"amend", "QA", "--expected-revision", "3", "--reason", "r", "--title", "T"}, "hadron question get <question>", true},
		{"withdraw", "WithdrawQuestion", "withdrawQuestion", []string{"withdraw", "QA", "--expected-revision", "3", "--reason", "r", "--yes"}, "hadron question get <question>", true},
		{"purge", "PurgeUnansweredQuestion", "purgeUnansweredQuestion", []string{"purge", "QA", "--reason", "r", "--yes"}, "hadron question get <question>", true},
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
			cases["an absent result with no error"] = struct {
				resp string
				exit int
			}{`{"data":{}}`, exitcode.Unavailable}
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

// cli#2854: the four writes are sent at most once whatever the transport does, and what the HTTP
// layer refused before any resolver ran is not an unknown outcome: a plain 401 or 404 keeps the
// ordinary mapping; a 5xx or a connection dropped after the request left is exit 7 "not confirmed";
// a connection that never opened is "could not reach the server" with no claim about the write.
func TestQuestionOtherWritesAreSentOnceAndMapTransportShapes2854(t *testing.T) {
	askFile := filepath.Join(t.TempDir(), "ask.json")
	if err := os.WriteFile(askFile, []byte(`{"title":"Pick a database"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	verbs := [][]string{
		{"ask", "LOG", "--input-file", askFile},
		{"amend", "QA", "--expected-revision", "3", "--reason", "r", "--title", "T"},
		{"withdraw", "QA", "--expected-revision", "3", "--reason", "r", "--yes"},
		{"purge", "QA", "--reason", "r", "--yes"},
	}
	status := func(code int, body string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		}
	}
	hangup := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}
	}
	for _, verb := range verbs {
		for name, tc := range map[string]struct {
			h       func(http.ResponseWriter, *http.Request)
			exit    int
			unknown bool
		}{
			"401 a refused credential":                 {status(401, `no`), exitcode.AuthRequired, false},
			"404 plain":                                {status(404, `gone`), exitcode.NotFound, false},
			"429 a rate limit":                         {status(429, `slow down`), exitcode.Error, false},
			"500 a plain page":                         {status(500, `oops`), exitcode.Unavailable, true},
			"502 a gateway page":                       {status(502, `<html>bad gateway</html>`), exitcode.Unavailable, true},
			"the connection dropped after the request": {hangup, exitcode.Unavailable, true},
		} {
			var n int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&n, 1)
				tc.h(w, r)
			}))
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"question"}, verb...), "--server", srv.URL))
			err := root.Execute()
			srv.Close()
			if err == nil || atomic.LoadInt32(&n) != 1 {
				t.Errorf("%s %s: want an error after exactly one request, got requests=%d err=%v", verb[0], name, atomic.LoadInt32(&n), err)
				continue
			}
			if exitCodeFor(err) != tc.exit {
				t.Errorf("%s %s: exit=%d want %d: %v", verb[0], name, exitCodeFor(err), tc.exit, err)
			}
			if has := strings.Contains(err.Error(), "not confirmed"); has != tc.unknown {
				t.Errorf("%s %s: 'not confirmed'=%v want %v: %v", verb[0], name, has, tc.unknown, err)
			}
			if tc.unknown && !strings.HasPrefix(err.Error(), map[string]string{"ask": "ask", "amend": "amend", "withdraw": "withdrawal", "purge": "purge"}[verb[0]]+" not confirmed") {
				t.Errorf("%s %s: the unknown-outcome message names its verb first: %v", verb[0], name, err)
			}
		}
		// A connection that never opened: nothing was sent, nothing is claimed about the write.
		url := neverOpenedServerURL
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{"question"}, verb...), "--server", url))
		err := root.Execute()
		if err == nil || strings.Contains(err.Error(), "not confirmed") || exitCodeFor(err) != exitcode.Unavailable || !strings.Contains(err.Error(), "could not reach") {
			t.Errorf("%s a connection that never opened: want exit 7 'could not reach' without 'not confirmed', got exit=%d %v", verb[0], exitCodeFor(err), err)
		}
	}
}

// An explicit false from purge is the server saying no, not an unknown outcome: exit 1, no read-back.
func TestQuestionPurgeAnsweredFalseIsAPlainRefusal2854(t *testing.T) {
	_, _, err := runQuestion(t, map[string]string{"PurgeUnansweredQuestion": `{"data":{"purgeUnansweredQuestion":false}}`}, "purge", "QA", "--reason", "r", "--yes")
	if exitCodeFor(err) != exitcode.Error || err == nil || strings.Contains(err.Error(), "not confirmed") {
		t.Errorf("purge false: want exit 1 without 'not confirmed', got exit=%d %v", exitCodeFor(err), err)
	}
}
