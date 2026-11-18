package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// server#4219 (entity refs S4) renamed the identifying argument of a family of
// mutations with no alias — a document that names a dropped arg fails "Unknown
// argument" — and collapsed the former optional *Id/*Ref pairs into one required
// *Ref. Pin the spelling in the documents the CLI sends (cli#2011).
func TestEntityRefsS4OperationsUseRenamedArgs2011(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		want     []string
		gone     []string
	}{
		{"JoinApp", gen.JoinApp_Operation, []string{"joinApp(appRef: $appRef"}, []string{"(appId:", "$appId"}},
		{"LeaveApp", gen.LeaveApp_Operation, []string{"leaveApp(appRef: $appRef"}, []string{"(appId:", "$appId"}},
		{"CreateRevision", gen.CreateRevision_Operation, []string{"createRevision(agentRef: $agentRef"}, []string{"agentId"}},
		{"AcquireEditorLock", gen.AcquireEditorLock_Operation, []string{"acquireEditorLock(agentRef: $agentRef"}, []string{"agentId"}},
		{"ReleaseEditorLock", gen.ReleaseEditorLock_Operation, []string{"releaseEditorLock(agentRef: $agentRef"}, []string{"agentId"}},
		{"DeleteMemory", gen.DeleteMemory_Operation, []string{"deleteMemory(ref: $ref"}, []string{"id: $id"}},
		{"SyncMemory", gen.SyncMemory_Operation, []string{"syncMemory(ref: $ref"}, []string{"id: $id"}},
		{"EncryptMemory", gen.EncryptMemory_Operation, []string{"memoryRef: $memoryRef"}, []string{"memoryId"}},
		{"LinkMemoryToUser", gen.LinkMemoryToUser_Operation, []string{"memoryRef: $memoryRef"}, []string{"memoryId"}},
		{"DeleteOrganization", gen.DeleteOrganization_Operation, []string{"deleteOrganization(ref: $ref"}, []string{"id: $id"}},
		{"CreateUserInvitation", gen.CreateUserInvitation_Operation, []string{"orgRef: $orgRef"}, []string{"orgId"}},
		{"CreateMemory", gen.CreateMemory_Operation, []string{"orgRef: $orgRef"}, []string{"orgId"}},
		{"CreateMemoryDraft", gen.CreateMemoryDraft_Operation, []string{"orgRef: $orgRef"}, []string{"orgId"}},
		{"AddOrgMember", gen.AddOrgMember_Operation, []string{"$orgRef: ID!", "$userRef: ID!"}, []string{"String!"}},
		{"EnsureAppMember", gen.EnsureAppMember_Operation, []string{"$appRef: ID!", "$userRef: ID!"}, []string{"$userRef: String"}},
	} {
		for _, w := range tc.want {
			if !strings.Contains(tc.op, w) {
				t.Errorf("%s: document lacks %q:\n%s", tc.name, w, tc.op)
			}
		}
		for _, g := range tc.gone {
			if strings.Contains(tc.op, g) {
				t.Errorf("%s: document still names the dropped %q", tc.name, g)
			}
		}
	}
}
