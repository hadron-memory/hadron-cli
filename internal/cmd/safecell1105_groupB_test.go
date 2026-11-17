package cmd

import (
	"strings"
	"testing"
)

// cli#1105 group B: webhook, usage, ticket, secret, schedule and run.

func webhook1105B(cell string) string {
	c := q1105(cell)
	return `{"id":` + c + `,"organizationId":"org1","appId":` + c + `,"agentId":null,"name":` + c + `,"enabled":true,
	"entryNodeUrn":` + c + `,"aiConfigName":null,"userId":null,"createdBy":"u1","argsSchema":null,"eventData":null,
	"policy":null,"lastCalledAt":` + c + `,"createdAt":"2026-07-05T00:00:00Z"}`
}

func schedule1105B(cell string) string {
	c := q1105(cell)
	return `{"id":` + c + `,"organizationId":"org1","appId":` + c + `,"agentId":null,"name":` + c + `,"cron":` + c + `,"timezone":` + c + `,"enabled":true,
	"entryNodeUrn":` + c + `,"aiConfigName":null,"userId":"u1","createdBy":"u1","eventData":null,"policy":null,
	"lastRunAt":null,"nextRunAt":` + c + `,"createdAt":"2026-07-05T00:00:00Z"}`
}

func secret1105B(cell string) string {
	c := q1105(cell)
	return `{"id":` + c + `,"ownerType":` + c + `,"ownerId":` + c + `,"name":` + c + `,"kind":` + c + `,
	"metadata":{"env":"dev"},"createdAt":` + c + `,"createdBy":"u1","updatedAt":"2026-07-15T00:00:00Z","updatedBy":"u1"}`
}

func run1105B(cell string) string {
	c := q1105(cell)
	return `{"id":` + c + `,"organizationId":` + c + `,"appId":` + c + `,"agentId":"agent1",
	"status":"COMPLETED","triggerKind":"MANUAL","triggerId":null,"entryNodeUrn":` + c + `,"curNodeUrn":` + c + `,
	"userId":"u1","createdBy":"u1","parentRunId":null,"attempts":1,"budgetActions":50,"budgetTokens":10000,"timeoutMs":60000,
	"policy":null,"eventData":null,"data":{"category":"urgent"},"failure":null,
	"createdAt":` + c + `,"startedAt":` + c + `,"finishedAt":` + c + `}`
}

func ticket1105B(cell string) string {
	c := q1105(cell)
	return `{"id":` + c + `,"organizationId":"org1","appId":null,"action":` + c + `,"mintedBy":"u1","note":null,
	"consumedByRunId":` + c + `,"consumedAt":null,"expiresAt":null,"createdAt":` + c + `}`
}

func usageEvent1105B(cell string) string {
	c := q1105(cell)
	return `{"id":"e1","type":` + c + `,"createdAt":` + c + `,"memoryId":` + c + `,"nodeLoc":` + c + `,"provider":` + c + `,"model":` + c + `,
	"keyOwnerType":null,"tokensIn":1,"tokensOut":3,"costMicroCents":null}`
}

func TestSafeCell1105BWebhook(t *testing.T) {
	creds := func(cell string) string {
		return `{"path":"/hooks/s3cr3t/x","token":"hpt-abc123","webhook":` + webhook1105B(cell) + `}`
	}
	assertSafeCell1105(t, safeCell1105Case{
		name: "webhook ls",
		args: []string{"webhook", "ls", "--app", "acme.com:ops"},
		resp: func(cell string) map[string]string {
			w := webhook1105B(cell)
			return map[string]string{"AgentWebhooks": `{"data":{"agentWebhooks":{"total":2,"items":[` + w + `,` + w + `]}}}`}
		},
		lines: 3,
	})
	assertSafeCell1105(t, safeCell1105Case{
		name: "webhook create",
		args: []string{"webhook", "create", "--app", "acme.com:ops", "--name", "x", "--entry", "acme.com::ops::tasks:on-deploy"},
		resp: func(cell string) map[string]string {
			return map[string]string{"CreateAgentWebhook": `{"data":{"createAgentWebhook":` + creds(cell) + `}}`}
		},
		lines: 7,
	})
	assertSafeCell1105(t, safeCell1105Case{
		name: "webhook rotate",
		args: []string{"webhook", "rotate", "wh1", "--yes"},
		resp: func(cell string) map[string]string {
			return map[string]string{"RotateAgentWebhook": `{"data":{"rotateAgentWebhook":` + creds(cell) + `}}`}
		},
		lines: 7,
	})
}

func TestSafeCell1105BUsage(t *testing.T) {
	assertSafeCell1105(t, safeCell1105Case{
		name: "usage events",
		args: []string{"usage", "events"},
		resp: func(cell string) map[string]string {
			e := usageEvent1105B(cell)
			return map[string]string{"UsageEventsPage": `{"data":{"usageEventsPage":{"total":2,"hasMore":false,"items":[` + e + `,` + e + `]}}}`}
		},
		lines: 3,
	})
	assertSafeCell1105(t, safeCell1105Case{
		name: "memory usage",
		args: []string{"memory", "usage", "acme.com:kb"},
		resp: func(cell string) map[string]string {
			c := q1105(cell)
			r := `{"nodeLoc":` + c + `,"type":` + c + `,"count":4}`
			return map[string]string{"MemoryUsageSummary": `{"data":{"memoryUsageSummary":[` + r + `,` + r + `]}}`}
		},
		lines: 3,
	})
}

func TestSafeCell1105BTicket(t *testing.T) {
	assertSafeCell1105(t, safeCell1105Case{
		name: "ticket ls",
		args: []string{"ticket", "ls", "--org", "acme.com"},
		resp: func(cell string) map[string]string {
			tk := ticket1105B(cell)
			return map[string]string{"ActionTickets": `{"data":{"actionTickets":{"total":2,"items":[` + tk + `,` + tk + `]}}}`}
		},
		lines: 3,
	})
	// The STATUS cell is "expires " + the server's expiry, so the cut is checked
	// without the exact-255 prefix the harness expects.
	for name, cell := range map[string]string{"hostile": hostile1105, "long": strings.Repeat("日", 1000)} {
		t.Run("ticket ls expiry/"+name, func(t *testing.T) {
			c := q1105(cell)
			tk := `{"id":"tkt1","organizationId":"org1","appId":null,"action":"a","mintedBy":"u1","note":null,"consumedByRunId":null,"consumedAt":null,"expiresAt":` + c + `,"createdAt":"2026-07-05T00:00:00Z"}`
			gql := fakeGraphQL(t, map[string]string{"ActionTickets": `{"data":{"actionTickets":{"total":2,"items":[` + tk + `,` + tk + `]}}}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"ticket", "ls", "--org", "acme.com", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			assertUncut1105B(t, out.String(), name == "long", 3)
		})
	}
	// mint echoes the --org flag, not a server string; the text line still must
	// not carry a control or hidden rune.
	t.Run("ticket mint", func(t *testing.T) {
		gql := fakeGraphQL(t, map[string]string{"MintActionTickets": `{"data":{"mintActionTickets":100}}`})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"ticket", "mint", "--org", hostile1105, "--action", "comm.outbound", "--count", "100", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		assertUncut1105B(t, out.String(), false, 1)
	})
}

// assertUncut1105B checks hidden runes and the line count and, when long, that
// no 256-rune run survived and a cut marker is present.
func assertUncut1105B(t *testing.T, text string, long bool, lines int) {
	t.Helper()
	if r, bad := hasHiddenRune1105(text); bad {
		t.Fatalf("hidden rune %U survived in %q", r, text)
	}
	if got := len(strings.Split(strings.TrimRight(text, "\n"), "\n")); got != lines {
		t.Fatalf("want %d lines, got %d:\n%q", lines, got, text)
	}
	if long && (strings.Contains(text, strings.Repeat("日", 256)) || !strings.Contains(text, "…")) {
		t.Fatalf("long value must be cut with …:\n%s", text)
	}
}

func TestSafeCell1105BSecret(t *testing.T) {
	assertSafeCell1105(t, safeCell1105Case{
		name: "secret ls",
		args: []string{"secret", "ls", "--scope", "user"},
		resp: func(cell string) map[string]string {
			s := secret1105B(cell)
			return map[string]string{"Secrets": `{"data":{"secrets":{"items":[` + s + `,` + s + `],"total":2}}}`}
		},
		lines: 3,
	})
	// The META cell is the server's raw metadata JSON, so it is checked on its
	// own: a bidi or zero-width rune inside a metadata string must not survive.
	for name, cell := range map[string]string{"hostile": hostile1105, "long": strings.Repeat("日", 1000)} {
		t.Run("secret ls metadata/"+name, func(t *testing.T) {
			s := `{"id":"s1","ownerType":"user","ownerId":"u1","name":"n","kind":"generic","metadata":{"env":` + q1105(cell) + `},
			"createdAt":"2026-07-15T00:00:00Z","createdBy":"u1","updatedAt":"2026-07-15T00:00:00Z","updatedBy":"u1"}`
			gql := fakeGraphQL(t, map[string]string{"Secrets": `{"data":{"secrets":{"items":[` + s + `,` + s + `],"total":2}}}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"secret", "ls", "--scope", "user", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			assertUncut1105B(t, out.String(), name == "long", 3)
		})
	}
	// create reads the value from stdin, which the shared harness does not feed.
	for name, cell := range map[string]string{"hostile": hostile1105, "long": strings.Repeat("日", 1000)} {
		t.Run("secret create/"+name, func(t *testing.T) {
			gql := fakeGraphQL(t, map[string]string{"CreateSecret": `{"data":{"createSecret":` + secret1105B(cell) + `}}`})
			f, out := testFactory(t)
			f.IOStreams.In = strings.NewReader("sk-secret-123\n")
			root := NewRootCmd(f)
			root.SetArgs([]string{"secret", "create", "--name", "n", "--scope", "user", "--kind", "generic", "--value-file", "-", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			text := out.String()
			assertUncut1105B(t, text, name == "long", 1)
			if name == "long" && !strings.Contains(text, strings.Repeat("日", 255)+"…") {
				t.Fatalf("want SafeCell's cut form:\n%s", text)
			}
		})
	}
}

func TestSafeCell1105BSchedule(t *testing.T) {
	assertSafeCell1105(t, safeCell1105Case{
		name: "schedule ls",
		args: []string{"schedule", "ls", "--app", "acme.com:ops"},
		resp: func(cell string) map[string]string {
			s := schedule1105B(cell)
			return map[string]string{"AgentSchedules": `{"data":{"agentSchedules":{"total":2,"items":[` + s + `,` + s + `]}}}`}
		},
		lines: 3,
	})
	assertSafeCell1105(t, safeCell1105Case{
		name: "schedule create",
		args: []string{"schedule", "create", "--app", "acme.com:ops", "--name", "n", "--cron", "0 6 * * *", "--entry", "acme.com::ops::tasks:digest"},
		resp: func(cell string) map[string]string {
			return map[string]string{"CreateAgentSchedule": `{"data":{"createAgentSchedule":` + schedule1105B(cell) + `}}`}
		},
		lines: 2,
	})
	assertSafeCell1105(t, safeCell1105Case{
		name: "schedule update",
		args: []string{"schedule", "update", "sch1", "--cron", "0 7 * * *"},
		resp: func(cell string) map[string]string {
			return map[string]string{"UpdateAgentSchedule": `{"data":{"updateAgentSchedule":` + schedule1105B(cell) + `}}`}
		},
		lines: 2,
	})
}

func TestSafeCell1105BRun(t *testing.T) {
	assertSafeCell1105(t, safeCell1105Case{
		name: "run ls",
		args: []string{"run", "ls", "--org", "acme.com"},
		resp: func(cell string) map[string]string {
			r := run1105B(cell)
			return map[string]string{"AppRuns": `{"data":{"appRuns":{"total":2,"items":[` + r + `,` + r + `]}}}`}
		},
		lines: 3,
	})
	assertSafeCell1105(t, safeCell1105Case{
		name: "run get",
		args: []string{"run", "get", "run1"},
		resp: func(cell string) map[string]string {
			return map[string]string{"AppRun": `{"data":{"appRun":` + run1105B(cell) + `}}`}
		},
		lines: 8,
	})
	assertSafeCell1105(t, safeCell1105Case{
		name: "run cancel",
		args: []string{"run", "cancel", "run1", "--yes"},
		resp: func(cell string) map[string]string {
			return map[string]string{"CancelAppRun": `{"data":{"cancelAppRun":` + run1105B(cell) + `}}`}
		},
		lines: 1,
	})
}
