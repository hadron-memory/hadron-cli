package cmd

import (
	"strings"
	"testing"
)

// cli#1105 review (Joost, Riana): `asset url` prints the uploader-authored
// filename to stderr on both the present- and absent-hotlink paths, so a
// hostile filename must reach neither raw.
func TestSafeCell1105DAssetURLFilenameStderr(t *testing.T) {
	for _, tc := range []struct{ name, publicURL string }{
		{"present hotlink", `"https://cdn/one.png"`},
		{"absent hotlink", "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql := fakeGraphQL(t, map[string]string{
				"GetMemory": assetMemoryResp,
				"MemoryAssets": assetListResp(1, false,
					assetJSON("a1", strings.Trim(q1105(hostile1105), `"`), "image/png", 10, "CLEAN", tc.publicURL)),
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"asset", "url", "hrn:asset:acme.com:kb:assets:a1", "--server", gql.URL})
			_ = root.Execute()
			stderr := f.IOStreams.ErrOut.(*strings.Builder).String()
			if !strings.Contains(stderr, "a") || stderr == "" {
				t.Fatalf("the command never reached the filename line: %q", stderr)
			}
			if r, bad := hasHiddenRune1105(stderr); bad {
				t.Fatalf("hidden rune %U survived in %q", r, stderr)
			}
			if got := len(strings.Split(strings.TrimRight(stderr, "\n"), "\n")); got != 1 {
				t.Fatalf("want one stderr line, got %d: %q", got, stderr)
			}
		})
	}
}

// cli#1105 fix head (Xylon #39167): `run get` data/failure are server-supplied
// detail values; the JSON is valid but carries literal format runes, which
// SafeCell neutralizes and cuts. --json keeps the original.
func TestSafeCell1105DRunGetDataFailure(t *testing.T) {
	runWith := func(data, failure string) func(string) map[string]string {
		return func(cell string) map[string]string {
			body := strings.Replace(run1105B(cell), `"data":{"category":"urgent"},"failure":null`,
				`"data":`+data+`,"failure":`+failure, 1)
			return map[string]string{"AppRun": `{"data":{"appRun":` + body + `}}`}
		}
	}
	bidi := "{\"k\":\"a\u202ed\u200be\"}"
	long := "{\"k\":\"" + strings.Repeat("日", 1000) + "\"}"
	c := safeCell1105Case{name: "run get", args: []string{"run", "get", "run1"}, lines: 8}

	t.Run("bidi", func(t *testing.T) {
		c.resp = runWith(bidi, bidi)
		text := runSafeCell1105(t, c, "x", false)
		if r, bad := hasHiddenRune1105(text); bad {
			t.Fatalf("hidden rune %U survived in %q", r, text)
		}
	})
	t.Run("long", func(t *testing.T) {
		c.resp = runWith(long, long)
		text := runSafeCell1105(t, c, "x", false)
		if strings.Contains(text, strings.Repeat("日", 256)) || strings.Count(text, "…") < 2 {
			t.Fatalf("data and failure must each be cut to 256 runes ending in …:\n%s", text)
		}
	})
	t.Run("json", func(t *testing.T) {
		c.resp = runWith(bidi, long)
		raw := runSafeCell1105(t, c, "x", true)
		if !strings.Contains(raw, "\u202e") || !strings.Contains(raw, strings.Repeat("日", 1000)) {
			t.Fatalf("--json must keep data/failure original and complete: %s", raw)
		}
	})
}

// Riana (#39181): wrap path and token per field, never the composed line. A
// realistic token (~190 runes) must pass byte-identical, so a silent cut at
// 256 is caught here rather than by a customer with an unusable credential.
func TestSafeCell1105DWebhookCredentials(t *testing.T) {
	realToken := "hpt-" + strings.Repeat("a1B2", 46) + "xy"
	realPath := "/hooks/" + strings.Repeat("s3cr", 30) + "/x"
	resp := func(path, token string) func(string) map[string]string {
		return func(cell string) map[string]string {
			return map[string]string{"CreateAgentWebhook": `{"data":{"createAgentWebhook":{"path":` + q1105(path) + `,"token":` + q1105(token) +
				`,"webhook":` + webhook1105B("n") + `}}}`}
		}
	}
	c := safeCell1105Case{name: "webhook create", lines: 7,
		args: []string{"webhook", "create", "--app", "acme.com:ops", "--name", "x", "--entry", "acme.com::ops::tasks:on-deploy"}}

	t.Run("hostile", func(t *testing.T) {
		c.resp = resp(hostile1105, hostile1105)
		text := runSafeCell1105(t, c, "x", false)
		if r, bad := hasHiddenRune1105(text); bad {
			t.Fatalf("hidden rune %U survived in %q", r, text)
		}
		if got := len(strings.Split(strings.TrimRight(text, "\n"), "\n")); got != c.lines {
			t.Fatalf("want %d lines, got %d:\n%q", c.lines, got, text)
		}
	})
	t.Run("realistic values unchanged", func(t *testing.T) {
		c.resp = resp(realPath, realToken)
		text := runSafeCell1105(t, c, "x", false)
		want := "  Call it as:     POST " + realPath + "?hpt=" + realToken + "\n"
		if !strings.Contains(text, want) || !strings.Contains(text, "platform token: "+realToken+"\n") {
			t.Fatalf("a realistic path and token must print byte-identical:\n%s", text)
		}
	})
}
