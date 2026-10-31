package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/secretfile"
)

func TestAppKey1353RevokeLostAnswer(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonMode], func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req map[string]any
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if opOf(req) != "RevokeAppKey" {
					t.Errorf("operation %v", req)
				}
				requests.Add(1)
				// The mutation was received, but no HTTP answer reaches the caller.
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
			}))
			defer srv.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"app", "key", "revoke", "key_9", "--yes", "--server", srv.URL}
			if jsonMode {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			err := root.Execute()
			if err == nil {
				t.Fatal("lost answer reported success")
			}
			if code := renderError(f, err); code != 7 {
				t.Fatalf("exit %d: %v", code, err)
			}
			msg := out.String() + f.IOStreams.ErrOut.(*strings.Builder).String()
			for _, want := range []string{"may already be revoked", "hadron app key list <app-ref>", "not retried"} {
				if !strings.Contains(msg, want) {
					t.Errorf("missing %q: %s", want, msg)
				}
			}
			if !strings.Contains(msg, "connection dropped") {
				t.Errorf("mapped transport diagnostic lost: %s", msg)
			}
			if jsonMode {
				var env struct {
					Error map[string]json.RawMessage `json:"error"`
				}
				if err := json.Unmarshal([]byte(out.String()), &env); err != nil {
					t.Fatal(err)
				}
				if string(env.Error["code"]) != "7" || env.Error["serverCode"] != nil || env.Error["message"] == nil {
					t.Fatalf("bad envelope: %s", out.String())
				}
				var message string
				if err := json.Unmarshal(env.Error["message"], &message); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(message, "may already be revoked") || !strings.Contains(message, "hadron app key list <app-ref>") || !strings.Contains(message, "not retried") || f.IOStreams.ErrOut.(*strings.Builder).Len() != 0 {
					t.Fatalf("guidance must be in JSON message only: %s", msg)
				}
			}
			if requests.Load() != 1 {
				t.Errorf("requests %d, want exactly one", requests.Load())
			}
		})
	}
}

func TestAppKey1353RevokeRefusalUnchanged(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{{"FORBIDDEN", 8}, {"UNAUTHENTICATED", 3}, {"BAD_USER_INPUT", 2}} {
		t.Run(tc.code, func(t *testing.T) {
			srv, n := keyServer(t, 200, `{"errors":[{"message":"refusal fixture","extensions":{"code":"`+tc.code+`"}}]}`)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"app", "key", "revoke", "key_9", "--yes", "--json", "--server", srv.URL})
			err := root.Execute()
			if err == nil {
				t.Fatal("refusal succeeded")
			}
			if code := renderError(f, err); code != tc.exit {
				t.Fatalf("exit %d: %v", code, err)
			}
			var env struct {
				Error struct {
					Code       int    `json:"code"`
					ServerCode string `json:"serverCode"`
					Message    string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(out.String()), &env); err != nil {
				t.Fatal(err, out.String())
			}
			if env.Error.Code != tc.exit || env.Error.ServerCode != tc.code || env.Error.Message != "refusal fixture" {
				t.Fatalf("changed refusal: %s", out.String())
			}
			if strings.Contains(out.String(), "may already") || n.Load() != 1 {
				t.Fatalf("refusal misclassified/retried: %s, %d", out.String(), n.Load())
			}
		})
	}
}

func TestAppKey1353RotateForce(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		success bool
	}{
		{"refusal", 200, `{"errors":[{"message":"no","extensions":{"code":"FORBIDDEN"}}]}`, false},
		{"lost-answer", 502, "gateway", false},
		{"success", 200, rotated1333, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, n := keyServer(t, tc.status, tc.body)
			dir := t.TempDir()
			dest := filepath.Join(dir, "key")
			previous := []byte("previous\x00key\nbytes")
			if err := os.WriteFile(dest, previous, 0o600); err != nil {
				t.Fatal(err)
			}
			r := runKeyArgs(t, srv, "rotate", app1223, "--out", dest, "--force", "--yes", "--json")
			if (r.err == nil) != tc.success {
				t.Fatalf("unexpected result: %v", r.err)
			}
			want := string(previous)
			if tc.success {
				want = rotatedRaw1333 + "\n"
			}
			got, err := os.ReadFile(dest)
			if err != nil || string(got) != want {
				t.Fatalf("destination not preserved/replaced correctly: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary file left behind: %v %v", entries, err)
			}
			if n.Load() != 1 || strings.Contains(r.all(), rotatedRaw1333) {
				t.Fatalf("retry or secret echo: count %d", n.Load())
			}
		})
	}
}

func TestAppKey1353RotateCommitFailure(t *testing.T) {
	for _, leftover := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleaned", true: "left-behind"}[leftover], func(t *testing.T) {
			// Package-wide seams: keep this test serial (no t.Parallel).
			oldWrite, oldRemove := secretfile.Write, secretfile.Remove
			t.Cleanup(func() { secretfile.Write, secretfile.Remove = oldWrite, oldRemove })
			secretfile.Write = func(f *os.File, b []byte) error {
				_, _ = f.Write(b[:4])
				return errors.New("injected " + rotatedRaw1333)
			}
			if leftover {
				secretfile.Remove = func(string) error { return errors.New("remove " + rotatedRaw1333) }
			}
			srv, n := keyServer(t, 200, rotated1333)
			dir := t.TempDir()
			dest := filepath.Join(dir, "key")
			if err := os.WriteFile(dest, []byte("previous"), 0o600); err != nil {
				t.Fatal(err)
			}
			r := runKeyArgs(t, srv, "rotate", app1223, "--out", dest, "--force", "--yes", "--json")
			if r.err == nil || exitCodeFor(r.err) != 1 {
				t.Fatalf("want error after rotation: %v", r.err)
			}
			msg := r.all()
			for _, want := range []string{"hadron app key list " + app1223, "rotate again", "revoked"} {
				if !strings.Contains(msg, want) {
					t.Errorf("missing %q: %s", want, msg)
				}
			}
			if strings.Contains(msg, rotatedRaw1333) || n.Load() != 1 {
				t.Fatalf("secret echo or retry: count %d", n.Load())
			}
			got, err := os.ReadFile(dest)
			if err != nil || string(got) != "previous" {
				t.Fatal("prior destination changed", err)
			}
			entries, _ := os.ReadDir(dir)
			wantFiles := 1
			if leftover {
				wantFiles = 2
				if !strings.Contains(msg, "delete it yourself") {
					t.Error("missing leftover warning")
				}
			}
			if len(entries) != wantFiles {
				t.Fatalf("files %d want %d", len(entries), wantFiles)
			}
		})
	}
}

// A transport class includes failures before the send. Keep uncertainty honest
// and the mapped diagnostic useful without echoing raw URLs or gateway bodies.
func TestAppKey1353RevokeTransportDiagnostics(t *testing.T) {
	for _, status := range []int{502, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv, n := keyServer(t, status, "gateway "+rotatedRaw1333)
			r := runKeyArgs(t, srv, "revoke", "key_9", "--yes")
			if r.err == nil || exitCodeFor(r.err) != 7 || n.Load() != 1 {
				t.Fatal(r.err, n.Load())
			}
			msg := r.all()
			for _, want := range []string{fmt.Sprintf("HTTP %d", status), "may have been sent", "may already be revoked", "app key list"} {
				if !strings.Contains(msg, want) {
					t.Errorf("missing %q: %s", want, msg)
				}
			}
			if strings.Contains(msg, rotatedRaw1333) {
				t.Fatal("raw gateway body echoed")
			}
		})
	}
	t.Run("before-send", func(t *testing.T) {
		srv, n := keyServer(t, 200, `{}`)
		srv.Close()
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "key", "revoke", "key_9", "--yes", "--server", srv.URL + "/" + rotatedRaw1333})
		err := root.Execute()
		if err == nil || renderError(f, err) != 7 || n.Load() != 0 {
			t.Fatal(err, n.Load())
		}
		msg := out.String() + f.IOStreams.ErrOut.(*strings.Builder).String()
		if strings.Contains(msg, "was sent") || !strings.Contains(msg, "may have been sent") || !strings.Contains(msg, "could not reach the server") || strings.Contains(msg, rotatedRaw1333) {
			t.Fatalf("dishonest or unsafe diagnostic: %s", msg)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			n.Add(1)
			<-r.Context().Done()
		}))
		defer srv.Close()
		f, out := testFactory(t)
		f.HTTPClient.Timeout = time.Second
		root := NewRootCmd(f)
		root.SetArgs([]string{"app", "key", "revoke", "key_9", "--yes", "--server", srv.URL})
		err := root.Execute()
		if err == nil || renderError(f, err) != 7 || n.Load() != 1 {
			t.Fatal(err, n.Load())
		}
		msg := out.String() + f.IOStreams.ErrOut.(*strings.Builder).String()
		if !strings.Contains(msg, "timed out") || !strings.Contains(msg, "app key list") {
			t.Fatal(msg)
		}
	})
}
