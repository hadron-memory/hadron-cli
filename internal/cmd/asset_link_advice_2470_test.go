package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Asset link sends only an asset reference, not a filename. The loopback
// provider fixture represents the stored asset's filename and the advice its
// reference-node write returns. This tests CLI display, not include detection.
func TestAssetLinkPlainPinAdvice2470(t *testing.T) {
	const target = "hrn:node:acme.com:kb:files:report"
	const ref = "hrn:asset:acme.com:kb:assets:a1"
	for _, filename := range []string{"report-{{> target rev=1}}.pdf", "report.pdf"} {
		for _, jsonMode := range []bool{false, true} {
			mode := "text"
			if jsonMode {
				mode = "json"
			}
			t.Run(filename+"/"+mode, func(t *testing.T) {
				run := func(advice bool) (string, string) {
					t.Helper()
					node := map[string]any{"id": "n1", "urn": target, "loc": "files:report", "name": filename, "nodeType": "reference", "memoryId": "mem1"}
					response := map[string]any{"data": map[string]any{"createAssetReferenceNode": node}}
					if advice {
						response["extensions"] = map[string]any{"warnings": []any{map[string]any{"path": "createAssetReferenceNode", "code": "include.pin.unkept", "text": "plain pin is not kept"}}}
					}
					body, err := json.Marshal(response)
					if err != nil {
						t.Fatal(err)
					}
					srv, requests := captureGraphQL(t, map[string]string{"CreateAssetReferenceNode": string(body)})
					f, out := testFactory(t)
					stderr := &strings.Builder{}
					f.IOStreams.ErrOut = stderr
					root := NewRootCmd(f)
					args := []string{"asset", "link", ref, "--node", target, "--server", srv.URL}
					if jsonMode {
						args = append(args, "--json")
					}
					root.SetArgs(args)
					if err := root.Execute(); err != nil {
						t.Fatalf("successful link became failure: %v", err)
					}
					vars := decodeVars(t, requests, "CreateAssetReferenceNode")
					if !reflect.DeepEqual(vars, map[string]any{"assetRef": ref, "nodeUrn": target}) {
						t.Fatalf("link inputs changed: %v", vars)
					}
					if jsonMode {
						var got map[string]any
						if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
							t.Fatal(err)
						}
						want := map[string]any{"nodeId": "n1", "nodeUrn": target, "loc": "files:report", "name": filename, "nodeType": "reference", "memoryId": "mem1", "assetId": "a1"}
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("JSON receipt changed: %v", got)
						}
					} else if want := "linked asset a1 as " + filename + "\n" + target + "\n"; out.String() != want {
						t.Fatalf("text receipt=%q want=%q", out.String(), want)
					}
					return out.String(), stderr.String()
				}
				baseline, quiet := run(false)
				if quiet != "" {
					t.Fatalf("no provider advice must be silent: %q", quiet)
				}
				if filename == "report.pdf" {
					return
				}
				actual, warning := run(true)
				if actual != baseline {
					t.Fatalf("advice changed stdout bytes: %q versus %q", actual, baseline)
				}
				if want := "warning: [include.pin.unkept] plain pin is not kept\n"; warning != want {
					t.Fatalf("stderr=%q want=%q", warning, want)
				}
			})
		}
	}
}
