package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#1105 group C: connection, billing, asset and the app agent roster
// (internal/approster). Email and config have their own tests below because
// their cells are quoted or local.

func two1105(row func(i string) string) string {
	return row("1") + "," + row("2")
}

func TestSafeCell1105CConnection(t *testing.T) {
	grant := func(i, cell string) string {
		return `{"id":` + q1105("g"+i+cell) + `,"connectionId":` + q1105(cell) + `,"granteeAppId":` + q1105(cell) +
			`,"granteeAppName":` + q1105(cell) + `,"granteeAppUrn":` + q1105(cell) + `,"scopes":[` + q1105(cell) + `],` +
			`"expiresAt":` + q1105(cell) + `,"createdAt":` + q1105(cell) + `}`
	}
	assertSafeCell1105(t, safeCell1105Case{
		name: "connection grant ls",
		args: []string{"connection", "grant", "ls"},
		resp: func(cell string) map[string]string {
			return map[string]string{"ConnectionGrants": `{"data":{"connectionGrants":{"items":[` +
				two1105(func(i string) string { return grant(i, cell) }) + `],"total":2}}}`}
		},
		lines: 3,
	})
	assertSafeCell1105(t, safeCell1105Case{
		name: "connection grant create",
		args: []string{"connection", "grant", "create", "--connection", "conn_123", "--app", "capp100000000000000000000", "--scopes", "mail.read"},
		resp: func(cell string) map[string]string {
			return map[string]string{"CreateConnectionGrant": `{"data":{"createConnectionGrant":` + grant("1", cell) + `}}`}
		},
		lines: 1,
	})
}

func TestSafeCell1105CBilling(t *testing.T) {
	assertSafeCell1105(t, safeCell1105Case{
		name: "billing prices",
		args: []string{"billing", "prices"},
		resp: func(cell string) map[string]string {
			row := func(i string) string {
				return `{"id":"p` + i + `","provider":` + q1105(cell) + `,"model":` + q1105(cell) + `,"tier":2,` +
					`"inputMicroCentsPerMTok":` + q1105(cell) + `,"outputMicroCentsPerMTok":` + q1105(cell) + `,` +
					`"markupBasisPoints":null,"effectiveAt":` + q1105(cell) + `,"createdAt":"2026-07-10T00:00:00.000Z","createdBy":null}`
			}
			return map[string]string{"AiModelPrices": `{"data":{"aiModelPrices":{"total":2,"items":[` + two1105(row) + `]}}}`}
		},
		lines: 3,
	})
	assertSafeCell1105(t, safeCell1105Case{
		name: "billing prices get",
		args: []string{"billing", "prices", "get", "p1"},
		resp: func(cell string) map[string]string {
			return map[string]string{"AiModelPrice": `{"data":{"aiModelPrice":{"id":` + q1105(cell) + `,"provider":` + q1105(cell) +
				`,"model":` + q1105(cell) + `,"tier":2,"inputMicroCentsPerMTok":` + q1105(cell) + `,"outputMicroCentsPerMTok":` + q1105(cell) +
				`,"markupBasisPoints":null,"effectiveAt":` + q1105(cell) + `,"createdAt":` + q1105(cell) + `,"createdBy":` + q1105(cell) + `}}}`}
		},
		lines: 11,
	})
	assertSafeCell1105(t, safeCell1105Case{
		name: "billing balance",
		args: []string{"billing", "balance", "org1"},
		resp: func(cell string) map[string]string {
			return map[string]string{"CreditBalance": `{"data":{"creditBalance":{"organizationId":` + q1105(cell) + `,"balanceMicroCents":` + q1105(cell) + `}}}`}
		},
		lines: 1,
	})
	assertSafeCell1105(t, safeCell1105Case{
		name: "billing ledger",
		args: []string{"billing", "ledger", "org1"},
		resp: func(cell string) map[string]string {
			row := func(i string) string {
				return `{"id":` + q1105("l"+i+cell) + `,"organizationId":"org1","kind":` + q1105(cell) + `,"amountMicroCents":` + q1105(cell) + `,` +
					`"expiresAt":` + q1105(cell) + `,"sourceType":null,"sourceId":null,"note":` + q1105(cell) + `,` +
					`"createdAt":` + q1105(cell) + `,"createdBy":null}`
			}
			return map[string]string{"CreditLedgerEntries": `{"data":{"creditLedgerEntries":{"total":2,"items":[` + two1105(row) + `]}}}`}
		},
		lines: 3,
	})
}

func TestSafeCell1105CAsset(t *testing.T) {
	dir := t.TempDir()
	row := func(i, cell string) string {
		return `{"id":` + q1105("a"+i+cell) + `,"urn":"hrn:asset:acme.com:kb:assets:a` + i + `","filename":` + q1105(cell) +
			`,"mimeType":` + q1105(cell) + `,"sizeBytes":2048,"description":null,"scanStatus":"BLOCKED","scanSignature":` + q1105(cell) +
			`,"uploadedAt":` + q1105(cell) + `,"uploadedBy":"u1","deletedAt":"2026-08-07T00:00:00Z","memoryId":"mem1","publicUrl":null}`
	}
	assertSafeCell1105(t, safeCell1105Case{
		name: "asset list",
		args: []string{"asset", "list", "-m", "acme.com::kb"},
		resp: func(cell string) map[string]string {
			return map[string]string{
				"GetMemory": assetMemoryResp,
				"MemoryAssets": `{"data":{"memoryAssets":{"total":2,"hasMore":false,"assets":[` +
					two1105(func(i string) string { return row(i, cell) }) + `]}}}`,
			}
		},
		lines: 3,
	})

	bytesSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("PNGDATA"))
	}))
	t.Cleanup(bytesSrv.Close)
	assertSafeCell1105(t, safeCell1105Case{
		name: "asset get",
		args: []string{"asset", "get", "a1", "-o", filepath.Join(dir, "out.png"), "--force"},
		resp: func(cell string) map[string]string {
			return map[string]string{"AssetDownloadUrl": `{"data":{"assetDownloadUrl":{"url":"` + bytesSrv.URL + `","filename":` + q1105(cell) +
				`,"mimeType":` + q1105(cell) + `,"sizeBytes":7,"expiresAt":"2026-08-06T00:05:00Z"}}}`}
		},
		lines: 1,
	})

	assertSafeCell1105(t, safeCell1105Case{
		name: "asset link",
		args: []string{"asset", "link", "a1", "--node", "hrn:node:acme.com:kb:designs:logo"},
		resp: func(cell string) map[string]string {
			return map[string]string{"CreateAssetReferenceNode": `{"data":{"createAssetReferenceNode":{"id":` + q1105(cell) + `,"urn":` + q1105(cell) +
				`,"loc":` + q1105(cell) + `,"name":` + q1105(cell) + `,"nodeType":"reference","memoryId":"mem1"}}}`}
		},
		lines: 2,
	})
	assertSafeCell1105(t, safeCell1105Case{
		name: "asset rm",
		args: []string{"asset", "rm", "a1", "--yes"},
		resp: func(cell string) map[string]string {
			return map[string]string{"SoftDeleteAsset": `{"data":{"softDeleteAsset":{"id":` + q1105(cell) + `,"urn":` + q1105(cell) +
				`,"filename":` + q1105(cell) + `,"deletedAt":"2026-08-06T00:00:00Z"}}}`}
		},
		lines: 1,
	})
	assertSafeCell1105(t, safeCell1105Case{
		name: "asset restore",
		args: []string{"asset", "restore", "a1"},
		resp: func(cell string) map[string]string {
			return map[string]string{"RestoreAsset": `{"data":{"restoreAsset":{"id":` + q1105(cell) + `,"urn":` + q1105(cell) +
				`,"filename":` + q1105(cell) + `,"deletedAt":null}}}`}
		},
		lines: 1,
	})

	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(store.Close)
	src := filepath.Join(t.TempDir(), "logo.png")
	if err := os.WriteFile(src, []byte("PNGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertSafeCell1105(t, safeCell1105Case{
		name: "asset upload",
		args: []string{"asset", "upload", src, "-m", "acme.com::kb"},
		resp: func(cell string) map[string]string {
			return map[string]string{
				"GetMemory": assetMemoryResp,
				"BeginAssetUpload": `{"data":{"beginAssetUploadV2":{"uploadId":"up1","putUrl":"` + store.URL +
					`","putHeaders":[{"name":"Content-Type","value":"image/png"}],"storageKey":"k","maxSizeBytes":10485760,` +
					`"allowedMimeType":"image/png","expiresAt":"2026-08-06T01:00:00Z"}}}`,
				"CompleteAssetUpload": `{"data":{"completeAssetUpload":{"id":"a1","urn":` + q1105(cell) + `,"filename":` + q1105(cell) +
					`,"mimeType":` + q1105(cell) + `,"sizeBytes":7,"scanStatus":"CLEAN","uploadedAt":"2026-08-06T00:00:00Z",` +
					`"memoryId":"mem1","publicUrl":null}}}`,
			}
		},
		lines: 2,
	})
}

func TestSafeCell1105CAppAgentList(t *testing.T) {
	assertSafeCell1105(t, safeCell1105Case{
		name: "app agent list",
		args: []string{"app", "agent", "list", "hrn:app:acme.com:support"},
		resp: func(cell string) map[string]string {
			agent := func(i string) string {
				return `{"id":` + q1105("agt"+i+cell) + `,"urn":` + q1105(cell) + `,"name":` + q1105(cell) + `,"description":null,` +
					`"visibility":"ORGANIZATION","organizationId":"o1","personaRole":` + q1105(cell) + `,"createdAt":"2026-08-11T00:00:00Z"}`
			}
			install := func(i string) string {
				return `{"agent":{"id":` + q1105("agt"+i+cell) + `},"bootPromptRef":` + q1105(cell) + `,"bootPromptTargetDeleted":true}`
			}
			return map[string]string{"AppAgentRoster": `{"data":{"app":{"id":"capp100000000000000000000","urn":` + q1105(cell) +
				`,"name":` + q1105(cell) + `,"agents":[` + two1105(agent) + `],"appAgents":[` + two1105(install) + `]}}}`}
		},
		lines: 4,
	})
}

// Email cells are strconv.Quote'd before SafeCell, so control and format runes
// are already escaped; what SafeCell adds there is the 256-rune cut (the quote
// takes one rune of the budget).
func TestSafeCell1105CEmailConnections(t *testing.T) {
	resp := func(cell string) map[string]string {
		row := func(i string) string {
			return `{"id":` + q1105("c"+i+cell) + `,"organizationId":` + q1105(cell) + `,"userId":` + q1105(cell) + `,"mailboxEmail":` + q1105(cell) +
				`,"provider":` + q1105(cell) + `,"displayName":` + q1105(cell) + `,"syncEnabled":false,"syncStatus":` + q1105(cell) +
				`,"lastSyncAt":` + q1105(cell) + `,"createdAt":` + q1105(cell) + `,"updatedAt":` + q1105(cell) + `}`
		}
		return map[string]string{"EmailConnections": `{"data":{"exchangeConnections":[` + two1105(row) + `]}}`}
	}
	c := safeCell1105Case{name: "email connections", args: []string{"email", "connections", "--org", "o"}, resp: resp, lines: 4}
	assertSafeCell1105Hostile := func(t *testing.T) {
		text := runSafeCell1105(t, c, hostile1105, false)
		if r, bad := hasHiddenRune1105(text); bad {
			t.Fatalf("hidden rune %U survived in %q", r, text)
		}
		if got := len(strings.Split(strings.TrimRight(text, "\n"), "\n")); got != c.lines {
			t.Fatalf("want %d lines, got %d:\n%q", c.lines, got, text)
		}
	}
	t.Run("hostile", assertSafeCell1105Hostile)
	t.Run("long", func(t *testing.T) {
		text := runSafeCell1105(t, c, strings.Repeat("日", 1000), false)
		if strings.Contains(text, strings.Repeat("日", 256)) || !strings.Contains(text, `"`+strings.Repeat("日", 254)+"…") {
			t.Fatalf("a quoted 1000-rune value must be cut to 256 runes ending in …:\n%s", text)
		}
	})
	t.Run("json", func(t *testing.T) {
		for _, cell := range []string{hostile1105, strings.Repeat("日", 1000)} {
			raw := runSafeCell1105(t, c, cell, true)
			var v any
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				t.Fatalf("--json is not JSON: %v\n%s", err, raw)
			}
			if !jsonCarries1105(v, cell) {
				t.Fatalf("--json must keep the original value, got %s", raw)
			}
		}
	})
}

// config list prints local values; seed one through `config set` and read it
// back in the same factory.
func TestSafeCell1105CConfigList(t *testing.T) {
	for name, cell := range map[string]string{"hostile": hostile1105, "long": strings.Repeat("日", 1000)} {
		t.Run(name, func(t *testing.T) {
			f, out := testFactory(t)
			set := NewRootCmd(f)
			set.SetArgs([]string{"config", "set", "spec_memory", cell})
			if err := set.Execute(); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			list := NewRootCmd(f)
			list.SetArgs([]string{"config", "list"})
			if err := list.Execute(); err != nil {
				t.Fatal(err)
			}
			text := out.String()
			if r, bad := hasHiddenRune1105(text); bad {
				t.Fatalf("hidden rune %U survived in %q", r, text)
			}
			if got := len(strings.Split(strings.TrimRight(text, "\n"), "\n")); got != 6 {
				t.Fatalf("want one line per config key (6), got %d:\n%q", got, text)
			}
			if name == "long" && (strings.Contains(text, strings.Repeat("日", 256)) || !strings.Contains(text, strings.Repeat("日", 255)+"…")) {
				t.Fatalf("long value must render in SafeCell's cut form:\n%s", text)
			}
			out.Reset()
			js := NewRootCmd(f)
			js.SetArgs([]string{"config", "list", "--json"})
			if err := js.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "spec_memory") {
				t.Fatalf("--json: %s", out.String())
			}
		})
	}
}
