package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// #1302 item 2: a refused copied-prefix --title names the offending prefix and
// shows the corrected title to retry with, on the ordinary binary path (stderr)
// and in the --json envelope, with exit 2 and no write.
func TestSpecNewDuplicatedTitleCorrectedHint(t *testing.T) {
	const wantHint = `--title "Burst allowance"`
	for _, tc := range []struct {
		name, title, prefix string
		args                []string
	}{
		{"original report", "qa:api:010:04 — Burst allowance", "qa:api:010:04 — ", []string{"qa:api:010:04"}},
		{"padded", "  qa:api:010:04 —   Burst allowance  ", "qa:api:010:04 — ", []string{"qa:api:010:04"}},
		{"copied twice", "qa:api:010:04 — qa:api:010:04 — Burst allowance", "qa:api:010:04 — ", []string{"qa:api:010:04"}},
		{"legacy allocation", "qat:api:010:04 — Burst allowance", "qat:api:010:04 — ", []string{"--product", "qat", "--module", "api", "--feature", "010", "--rule", "04"}},
		{"new path", "msg:010:04 — Burst allowance", "msg:010:04 — ", []string{"msg:010:04", "--new-path"}},
	} {
		for _, jsonOut := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: " stderr", true: " json"}[jsonOut], func(t *testing.T) {
				url, captured := spec1148Server(t)
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := append([]string{"spec", "new"}, tc.args...)
				args = append(args, "-m", specMem, "--title", tc.title, "--no-edges", "--server", url)
				if jsonOut {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				err := root.Execute()
				if err == nil {
					t.Fatal("copied prefix was accepted")
				}
				if code := renderFailure(f, args, err); code != 2 {
					t.Fatalf("exit %d, want 2: %v", code, err)
				}
				for _, op := range []string{"CreateSpecNode", "CreateNode", "CreateEdge"} {
					if _, ok := captured[op]; ok {
						t.Fatalf("refusal wrote via %s", op)
					}
				}
				msg := f.IOStreams.ErrOut.(*strings.Builder).String()
				if jsonOut {
					var env struct {
						Error struct {
							Code    int    `json:"code"`
							Message string `json:"message"`
						} `json:"error"`
					}
					if e := json.Unmarshal([]byte(out.String()), &env); e != nil {
						t.Fatalf("no JSON envelope: %v\n%s", e, out.String())
					}
					if env.Error.Code != 2 {
						t.Errorf("envelope code %d, want 2", env.Error.Code)
					}
					msg = env.Error.Message
				} else if out.String() != "" {
					t.Errorf("refusal printed to stdout: %s", out.String())
				}
				if !strings.Contains(msg, `"`+tc.prefix+`"`) || !strings.Contains(msg, "drop it") || !strings.Contains(msg, wantHint) {
					t.Errorf("refusal %q must name %q and suggest %s", msg, tc.prefix, wantHint)
				}
			})
		}
	}
}
