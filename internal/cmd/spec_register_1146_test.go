package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cli#1146: `spec supersede` reminds about the register only for a corpus that
// has a register node (that is what makes it legacy); `spec register` labels
// the module :000 and the product :gen as general provisions.

// registerProxy fronts the supersede fake: a ResolveUrn for the register node
// gets registerAnswer, everything else goes to the inner fake. It counts the
// writes so a failed optional lookup can be shown not to retry the supersede.
func registerProxy(t *testing.T, registerAnswer string) (*httptest.Server, *map[string]int) {
	t.Helper()
	inner, _ := captureSupersedeGraphQL(t, "msg:010:03", supersedeRawMocks(cleanSpecDetailContent, cleanSpecDetailContent))
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				URN string `json:"urn"`
			} `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		calls[req.OperationName]++
		w.Header().Set("Content-Type", "application/json")
		if req.OperationName == "ResolveUrn" && strings.HasSuffix(req.Variables.URN, "register") {
			_, _ = w.Write([]byte(registerAnswer))
			return
		}
		resp, err := http.Post(inner.URL, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func supersedeStderr(t *testing.T, registerAnswer string) (stdout, stderr string, calls map[string]int) {
	t.Helper()
	srv, c := registerProxy(t, registerAnswer)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "supersede", "msg:010:02", "-m", specMem, "--title", "W2 v2", "--yes", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a committed supersede must succeed: %v\n%s", err, out.String())
	}
	return out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), *c
}

func TestSpecSupersedeNoRegisterNodeNoReminder(t *testing.T) {
	out, stderr, _ := supersedeStderr(t, resolveNullJSON)
	if strings.Contains(stderr, "register") || strings.Contains(stderr, "reminder") {
		t.Fatalf("no register node, so no reminder:\n%s", stderr)
	}
	if !strings.Contains(out, `"retired": true`) && !strings.Contains(out, `"retired":true`) {
		t.Fatalf("the supersede must still complete:\n%s", out)
	}
}

func TestSpecSupersedeWithRegisterNodeStillReminds(t *testing.T) {
	_, stderr, _ := supersedeStderr(t, resolveSpecJSON)
	if !strings.Contains(stderr, "reminder: update the register — mark msg:010:02 retired and add msg:010:03") {
		t.Fatalf("a legacy corpus keeps the reminder:\n%s", stderr)
	}
}

// A failed optional lookup after the write is reported honestly, neither
// failing the committed supersede nor retrying it.
func TestSpecSupersedeRegisterLookupFailureIsNotAFailedSupersede(t *testing.T) {
	_, stderr, calls := supersedeStderr(t, `{"errors":[{"message":"lookup boom"}]}`)
	if !strings.Contains(stderr, "could not check whether this corpus has a register") || !strings.Contains(stderr, "committed") {
		t.Fatalf("expected an honest note:\n%s", stderr)
	}
	if strings.Contains(stderr, "reminder: update the register") {
		t.Fatalf("an unknown register must not claim one exists:\n%s", stderr)
	}
	if calls["UpdateSpecNode"] != 1 || calls["CreateSpecNode"] != 1 {
		t.Fatalf("the supersede must not be retried: %v", calls)
	}
}

func registerGeneralFixture(t *testing.T, args ...string) string {
	t.Helper()
	nodes := []string{"cli", "cli:gen", "cli:cha", "cli:cha:000", "cli:cha:010", "cli:cha:010:01"}
	var list []string
	for _, loc := range nodes {
		list = append(list, specNodeList(loc, `["spec"]`))
	}
	gql := fakeGraphQL(t, map[string]string{
		"FindNodes":  `{"data":{"nodes":[` + strings.Join(list, ",") + `]}}`,
		"ResolveUrn": resolveNullJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"spec", "register", "-m", specMem, "--server", gql.URL}, args...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestSpecRegisterLabelsGeneralProvisions(t *testing.T) {
	text := registerGeneralFixture(t)
	for _, want := range []string{
		"cli:gen  general provisions (product)\n",
		"  cli:cha:000  general provisions\n",
		"  cli:cha:010  rules: 01  (next rule: 02)\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "cli:cha:000  rules") || strings.Contains(text, "cli:cha:000  general provisions  (next") {
		t.Errorf(":000 must not read as an allocatable feature:\n%s", text)
	}
}

func TestSpecRegisterGeneralProvisionsJSONIsAdditive(t *testing.T) {
	var dto struct {
		ProductGeneral []string `json:"productGeneral"`
		Modules        []struct {
			Features []map[string]any `json:"features"`
		} `json:"modules"`
	}
	if err := json.Unmarshal([]byte(registerGeneralFixture(t, "--json")), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.ProductGeneral) != 1 || dto.ProductGeneral[0] != "cli:gen" {
		t.Fatalf("productGeneral: %v", dto.ProductGeneral)
	}
	feats := dto.Modules[0].Features
	if len(feats) != 2 || feats[0]["feature"] != "000" || feats[0]["generalProvisions"] != true || feats[1]["generalProvisions"] != false {
		t.Fatalf("features: %v", feats)
	}
	for _, fe := range feats {
		for _, k := range []string{"feature", "rules", "nextRule", "replacedRules"} {
			if _, ok := fe[k]; !ok {
				t.Errorf("existing key %q dropped: %v", k, fe)
			}
		}
	}
	if feats[1]["nextRule"] != "02" {
		t.Errorf("nextRule changed: %v", feats[1])
	}
}
