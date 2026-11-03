package cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestReplace1227EmptyResults(t *testing.T) {
	for _, wire := range []string{"[]", "null"} {
		for _, dry := range []bool{true, false} {
			for _, jsonOutput := range []bool{true, false} {
				t.Run(fmt.Sprintf("wire=%s/dry=%t/json=%t", wire, dry, jsonOutput), func(t *testing.T) {
					gql, _ := captureGraphQL(t, map[string]string{
						"SearchReplaceInNodes": fmt.Sprintf(`{"data":{"searchReplaceInNodes":{"nodesScanned":3,"nodesChanged":0,"totalReplacements":0,"dryRun":true,"results":%s}}}`, wire),
					})
					f, out := testFactory(t)
					stderr := &strings.Builder{}
					f.IOStreams.ErrOut = stderr
					root := NewRootCmd(f)
					args := []string{"replace", "text", "absent-1227", "replacement", "-m", "acme.com::kb", "--field", "content", "--server", gql.URL}
					if dry {
						args = append(args, "--dry-run")
					} else {
						args = append(args, "--yes")
					}
					if jsonOutput {
						args = append(args, "--json")
					}
					root.SetArgs(args)
					if err := root.Execute(); err != nil {
						t.Fatal(err)
					}
					if jsonOutput {
						var got map[string]json.RawMessage
						if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
							t.Fatalf("decode stdout: %v: %s", err, out.String())
						}
						want := map[string]string{"nodesScanned": "3", "nodesChanged": "0", "totalReplacements": "0", "dryRun": fmt.Sprint(dry), "results": "[]"}
						if len(got) != len(want) {
							t.Fatalf("unexpected JSON keys: %s", out.String())
						}
						for key, value := range want {
							if string(got[key]) != value {
								t.Errorf("%s = %s, want %s", key, got[key], value)
							}
						}
					} else {
						want := "Replaced 0 occurrence(s) across 0 of 3 node(s) scanned.\n"
						if dry {
							want = "Would replace 0 occurrence(s) across 0 of 3 node(s) scanned (dry run — nothing written).\n"
						}
						if out.String() != want {
							t.Fatalf("stdout = %q, want %q", out.String(), want)
						}
					}
					wantStderr := ""
					if !dry {
						wantStderr = "No matches — nothing to replace.\n"
					}
					if stderr.String() != wantStderr {
						t.Errorf("stderr = %q, want %q", stderr.String(), wantStderr)
					}
				})
			}
		}
	}
}

func TestReplace1227ChangedResultsStaySorted(t *testing.T) {
	const unsorted = `[{"nodeId":"nz","loc":"z","memoryId":"mem1","replacements":1,"fields":[{"field":"name","matches":1}]},{"nodeId":"na","loc":"a","memoryId":"mem1","replacements":2,"fields":[{"field":"content","matches":2}]}]`
	const sorted = `[{"nodeId":"na","loc":"a","memoryId":"mem1","replacements":2,"fields":[{"field":"content","matches":2}]},{"nodeId":"nz","loc":"z","memoryId":"mem1","replacements":1,"fields":[{"field":"name","matches":1}]}]`
	for _, dry := range []bool{true, false} {
		t.Run(fmt.Sprintf("dry=%t", dry), func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{
				"SearchReplaceInNodes": fmt.Sprintf(`{"data":{"searchReplaceInNodes":{"nodesScanned":3,"nodesChanged":2,"totalReplacements":3,"dryRun":%t,"results":%s}}}`, dry, unsorted),
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"replace", "text", "cat", "dog", "-m", "acme.com::kb", "--field", "content", "--json", "--server", gql.URL}
			if dry {
				args = append(args, "--dry-run")
			} else {
				args = append(args, "--yes")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var got, want any
			if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(fmt.Sprintf(`{"nodesScanned":3,"nodesChanged":2,"totalReplacements":3,"dryRun":%t,"results":%s}`, dry, sorted)), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("stdout = %s, want sorted results with counts/identities/fields preserved", out.String())
			}
		})
	}
}
