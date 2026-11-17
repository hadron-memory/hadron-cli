package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const grantRefusal2308 = "hadron: schedule preview refused: a worker needs a signed grant from a person to set up this automation. Ask a person who manages the App to grant it. No write was attempted."

func grantReply2308(hash, summary, reason string) string {
	return `{"data":{"previewAgentSchedule":{"outcome":"GRANT_REQUIRED","hash":` + hash + `,"summary":` + summary + `,"reason":` + reason + `}}}`
}

// A1: GRANT_REQUIRED is the server's authority refusal: exit 8, the refusal
// line on stderr, one preview call and no create call.
func TestSchedulePreviewGrantRequired2308Text(t *testing.T) {
	out, stderr, calls, err := runPreview1678(t, grantReply2308("null", "null", "null"), previewArgs1678()...)
	if exitCodeFor(err) != 8 || len(calls) != 1 || calls[0]["operationName"] != "PreviewAgentSchedule" {
		t.Fatalf("exit %d err %v calls %v", exitCodeFor(err), err, calls)
	}
	if strings.TrimSpace(stderr) != grantRefusal2308 || strings.Contains(stderr, "malformed") || strings.TrimSpace(out) != "" {
		t.Fatalf("stderr %q stdout %q", stderr, out)
	}
	for _, c := range calls {
		if c["operationName"] == "CreateAgentSchedule" {
			t.Fatal("a create was attempted")
		}
	}
}

// A2: --json carries the same refusal in the error envelope.
func TestSchedulePreviewGrantRequired2308JSON(t *testing.T) {
	out, stderr, _, err := runPreview1678(t, grantReply2308("null", "null", "null"), append(previewArgs1678(), "--json")...)
	if exitCodeFor(err) != 8 || strings.TrimSpace(stderr) != "" {
		t.Fatalf("exit %d stderr %q", exitCodeFor(err), stderr)
	}
	var env struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if e := json.Unmarshal([]byte(out), &env); e != nil {
		t.Fatal(e, out)
	}
	if env.Error.Code != 8 || env.Error.Message != strings.TrimPrefix(grantRefusal2308, "hadron: ") {
		t.Fatal(out)
	}
}

// A3: the outcome's specified shape is all-null; anything else stays malformed.
func TestSchedulePreviewGrantRequired2308ShapeGuard(t *testing.T) {
	for name, reply := range map[string]string{
		"hash":    grantReply2308(`"`+headHash1678+`"`, "null", "null"),
		"summary": grantReply2308("null", summary1678, "null"),
		"reason":  grantReply2308("null", "null", `"OTHER"`),
	} {
		_, stderr, calls, err := runPreview1678(t, reply, previewArgs1678()...)
		if exitCodeFor(err) != 7 || len(calls) != 1 || !strings.Contains(stderr, "malformed") {
			t.Fatalf("%s: exit %d %q", name, exitCodeFor(err), stderr)
		}
	}
}

// A4: the neighbours stay pinned.
func TestSchedulePreviewGrantRequired2308Neighbours(t *testing.T) {
	if _, _, _, err := runPreview1678(t, reply1678(), previewArgs1678()...); err != nil {
		t.Fatal(err)
	}
	cannot := `{"data":{"previewAgentSchedule":{"outcome":"CANNOT_BE_ADOPTED","reason":"OTHER","hash":null,"summary":null}}}`
	if _, _, _, err := runPreview1678(t, cannot, previewArgs1678()...); exitCodeFor(err) != 5 {
		t.Fatal(err)
	}
	unknown := `{"data":{"previewAgentSchedule":{"outcome":"SOMETHING_NEW","hash":null,"summary":null,"reason":null}}}`
	if _, _, _, err := runPreview1678(t, unknown, previewArgs1678()...); exitCodeFor(err) != 7 {
		t.Fatal(err)
	}
}

// A5: create's typed refusal exits 8, with the server's message on stderr.
func TestScheduleCreateGrantRequired2308(t *testing.T) {
	const msg = "A worker needs a signed grant from a person to set up this automation. Ask a person who manages the App to grant it."
	reply, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": msg, "extensions": map[string]any{"code": "TRIGGER_GRANT_REQUIRED"}}}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(reply)
	}))
	defer srv.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"schedule", "create", "--server", srv.URL, "--app", "acme.com:ops", "--entry", "acme.com::ops::entry", "--cron", "0 6 * * *", "--name", "n"})
	err := root.Execute()
	if err != nil {
		renderError(f, err)
	}
	if exitCodeFor(err) != 8 {
		t.Fatalf("exit %d: %v", exitCodeFor(err), err)
	}
	stderr := f.IOStreams.ErrOut.(*strings.Builder).String()
	if strings.TrimSpace(stderr) != "hadron: "+msg || strings.TrimSpace(out.String()) != "" {
		t.Fatalf("stderr %q stdout %q", stderr, out.String())
	}
}
