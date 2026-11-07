package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestSpecMintStaleAbstractSummaryScope1807(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("dryRun%t/json%t", dryRun, asJSON), func(t *testing.T) {
				var mu sync.Mutex
				var calls []bool
				srv := newVarServer(t, func(op string, raw json.RawMessage) string {
					switch op {
					case "Memories":
						return memListJSON
					case "MintMemoryNodes":
						var v struct {
							DryRun bool `json:"dryRun"`
						}
						if err := json.Unmarshal(raw, &v); err != nil {
							t.Error(err)
						}
						mu.Lock()
						calls = append(calls, v.DryRun)
						mu.Unlock()
						return fmt.Sprintf(`{"data":{"mintMemoryNodes":{"memoryId":"mem1","dryRun":%t,"minted":%t,"mintedCount":0,"mintLocs":[],"blockers":[],"staleAbstracts":[{"kind":"STALE_ABSTRACT","loc":"cor:one","message":"first stale abstract"},{"kind":"STALE_ABSTRACT","loc":"cor:two","message":"second stale abstract"}],"staleAbstractsBlock":false,"openQuestions":[]}}}`, v.DryRun, !v.DryRun)
					default:
						t.Errorf("unexpected operation %s", op)
						return `{}`
					}
				})
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"spec", "mint", "-m", "mem1", "--server", srv.URL}
				if dryRun {
					args = append(args, "--dry-run")
				} else {
					args = append(args, "--yes")
				}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				gotCalls := append([]bool(nil), calls...)
				mu.Unlock()
				wantCalls := 1
				if !dryRun {
					wantCalls = 2
				}
				if len(gotCalls) != wantCalls || !gotCalls[0] || (!dryRun && gotCalls[1]) {
					t.Fatalf("mint dryRun calls %v", gotCalls)
				}
				if asJSON {
					var report struct {
						Memory string `json:"memory"`
						Stale  []any  `json:"staleAbstracts"`
						DryRun bool   `json:"dryRun"`
					}
					if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
						t.Fatal(err)
					}
					if len(report.Stale) != 2 || report.DryRun != dryRun {
						t.Fatalf("report %s", out.String())
					}
					if strings.Contains(out.String(), "whole-memory audit") {
						t.Fatal("human pointer changed JSON")
					}
				} else {
					want := "  stale abstracts: 2 (reported only) — this mint's scope only; whole-memory audit: hadron spec validate -m hadronmemory.com::platform-specs\n"
					if strings.Count(out.String(), want) != 1 {
						t.Fatalf("missing scoped summary %q in:\n%s", want, out.String())
					}
				}
			})
		}
	}
}
