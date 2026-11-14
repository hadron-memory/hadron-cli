package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const personalGrant2021 = `{"id":"g1","appId":"app-other","action":"node.read","values":{"node":"n1"},"holderKind":"USER","holderId":"u1","startsAt":null,"expiresAt":"2026-11-01T00:00:00Z","maxUses":2,"useCount":2,"reason":"Past terms","createdAt":"2026-10-01T00:00:00Z","activatedAt":"2026-10-02T00:00:00Z","revokedAt":null,"allows":false,"signatureStatus":{"state":"EARLIER","integrityProblems":0}}`

func TestAuthGrantsPersonNotWorker2021(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"MyGrants": `{"data":{"myGrants":{"items":[` + personalGrant2021 + `],"total":3}}}`}, "auth", "grants", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].Op != "MyGrants" || (*calls)[0].Session != "" {
		t.Fatalf("person read became worker/preflight: %+v", *calls)
	}
	if (*calls)[0].Vars["limit"] != float64(20) || (*calls)[0].Vars["offset"] != float64(0) {
		t.Fatalf("paging: %+v", (*calls)[0].Vars)
	}
	if strings.Contains(out, `"state":"expired"`) || strings.Contains(out, `"state":"revoked"`) {
		t.Fatalf("derived lifecycle: %s", out)
	}
	var dto struct {
		Items []struct {
			ID, AppID       string
			Allows          bool
			ExpiresAt       *string
			SignatureStatus struct{ State *string }
		}
		Total int
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Total != 3 || len(dto.Items) != 1 || dto.Items[0].AppID != "app-other" || dto.Items[0].Allows || dto.Items[0].SignatureStatus.State == nil || *dto.Items[0].SignatureStatus.State != "EARLIER" || dto.Items[0].ExpiresAt == nil {
		t.Fatalf("server facts changed: %s", out)
	}
}

func TestAuthGrantsEmptyDoesNotProveNone2021(t *testing.T) {
	for _, jsonOut := range []bool{false, true} {
		args := []string{"auth", "grants"}
		if jsonOut {
			args = append(args, "--json")
		}
		calls, out, err := runTeamTask(t, map[string]string{"MyGrants": `{"data":{"myGrants":{"items":[],"total":0}}}`}, args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 1 || (*calls)[0].Session != "" {
			t.Fatalf("wrong identity: %+v", *calls)
		}
		if jsonOut {
			if !strings.Contains(out, `"items": []`) || strings.Contains(out, `"items": null`) {
				t.Fatalf("empty JSON shape: %s", out)
			}
		} else if strings.Contains(strings.ToLower(out), "you have no grants") || !strings.Contains(out, "outside the person audience") {
			t.Fatalf("overclaimed empty: %s", out)
		}
	}
}

func TestAuthGrantsPagingAndCount2021(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"MyGrants": `{"data":{"myGrants":{"items":[],"total":37}}}`}, "auth", "grants", "--limit", "0", "--offset", "17", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].Vars["limit"] != float64(0) || (*calls)[0].Vars["offset"] != float64(17) {
		t.Fatalf("count request: %+v", *calls)
	}
	var dto map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto) != 1 || string(dto["total"]) != "37" {
		t.Fatalf("count-only shape: %s", out)
	}
}

func TestAuthGrantsBoundsBeforeRequest2021(t *testing.T) {
	for _, args := range [][]string{{"--limit", "-1"}, {"--limit", "201"}, {"--offset", "-1"}, {"--offset", "2147483648"}} {
		calls, _, err := runTeamTask(t, map[string]string{}, append([]string{"auth", "grants"}, args...)...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 || !strings.Contains(err.Error(), "--limit takes 0..200") {
			t.Fatalf("bounds: args=%v err=%v calls=%+v", args, err, *calls)
		}
	}
}
