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
//
// The table is EVERY renamed operation the CLI has a document for (cli#2679);
// the renamed schema operations with no CLI document — clearAgentAIConfig,
// updateAgentAIConfig, pushMemoryToGit, startChat, revokeAgentSubscription —
// have no constant to pin and are covered by the coverage matrix's gap lines
// instead. UpdateOrgGithubApp's $appId: String! is GitHub's app id, not the
// renamed appRef, so its gone-list names orgId only.
func TestEntityRefsS4OperationsUseRenamedArgs2011(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		want     []string
		gone     []string
	}{
		{"JoinApp", gen.JoinApp_Operation, []string{"joinApp(appRef: $appRef"}, []string{"(appId:", "$appId"}},
		{"LeaveApp", gen.LeaveApp_Operation, []string{"leaveApp(appRef: $appRef"}, []string{"(appId:", "$appId"}},
		{"LeaveAppInstructed", gen.LeaveAppInstructed_Operation, []string{"leaveApp(appRef: $appRef"}, []string{"(appId:", "$appId"}},
		{"CreateRevision", gen.CreateRevision_Operation, []string{"createRevision(agentRef: $agentRef"}, []string{"agentId"}},
		{"PublishRevision", gen.PublishRevision_Operation, []string{"publishRevision(agentRef: $agentRef"}, []string{"agentId"}},
		{"RestoreRevision", gen.RestoreRevision_Operation, []string{"restoreRevision(agentRef: $agentRef"}, []string{"agentId"}},
		{"AcquireEditorLock", gen.AcquireEditorLock_Operation, []string{"acquireEditorLock(agentRef: $agentRef"}, []string{"agentId"}},
		{"ReleaseEditorLock", gen.ReleaseEditorLock_Operation, []string{"releaseEditorLock(agentRef: $agentRef"}, []string{"agentId"}},
		{"DeleteMemory", gen.DeleteMemory_Operation, []string{"deleteMemory(ref: $ref"}, []string{"id: $id"}},
		{"SyncMemory", gen.SyncMemory_Operation, []string{"syncMemory(ref: $ref"}, []string{"id: $id"}},
		{"SetMemorySourceToken", gen.SetMemorySourceToken_Operation, []string{"setMemorySourceToken(ref: $ref"}, []string{"id: $id"}},
		{"EncryptMemory", gen.EncryptMemory_Operation, []string{"memoryRef: $memoryRef"}, []string{"memoryId"}},
		{"LinkMemoryToUser", gen.LinkMemoryToUser_Operation, []string{"memoryRef: $memoryRef"}, []string{"memoryId"}},
		{"CreateMemorySubscription", gen.CreateMemorySubscription_Operation, []string{"createMemorySubscription(memoryRef: $memoryRef", "orgRef: $orgRef"}, []string{"memoryId", "orgId"}},
		{"UpdateMemorySubscription", gen.UpdateMemorySubscription_Operation, []string{"updateMemorySubscription(memoryRef: $memoryRef", "orgRef: $orgRef"}, []string{"memoryId", "orgId"}},
		{"DeleteMemorySubscription", gen.DeleteMemorySubscription_Operation, []string{"deleteMemorySubscription(memoryRef: $memoryRef", "orgRef: $orgRef"}, []string{"memoryId", "orgId"}},
		{"DeleteOrganization", gen.DeleteOrganization_Operation, []string{"deleteOrganization(ref: $ref"}, []string{"id: $id"}},
		{"CreateUserInvitation", gen.CreateUserInvitation_Operation, []string{"orgRef: $orgRef"}, []string{"orgId"}},
		{"CreateMemory", gen.CreateMemory_Operation, []string{"orgRef: $orgRef"}, []string{"orgId"}},
		{"CreateMemoryDraft", gen.CreateMemoryDraft_Operation, []string{"orgRef: $orgRef"}, []string{"orgId"}},
		{"LinkGithubApp", gen.LinkGithubApp_Operation, []string{"linkGithubApp(orgRef: $orgRef"}, []string{"orgId"}},
		{"UnlinkGithubApp", gen.UnlinkGithubApp_Operation, []string{"unlinkGithubApp(orgRef: $orgRef"}, []string{"orgId"}},
		{"UpdateOrgGithubApp", gen.UpdateOrgGithubApp_Operation, []string{"updateOrgGithubApp(orgRef: $orgRef"}, []string{"orgId"}},
		{"AddOrgMember", gen.AddOrgMember_Operation, []string{"$orgRef: ID!", "$userRef: ID!"}, []string{"String!"}},
		{"EnsureAppMember", gen.EnsureAppMember_Operation, []string{"$appRef: ID!", "$userRef: ID!"}, []string{"$userRef: String", "$appRef: String"}},
		{"EnsureAppMemberInstructed", gen.EnsureAppMemberInstructed_Operation, []string{"ensureAppMember(appRef: $appRef", "$userRef: ID!"}, []string{"$userRef: String", "$appRef: String"}},
		{"RemoveAppMember", gen.RemoveAppMember_Operation, []string{"removeAppMember(appRef: $appRef", "$userRef: ID!"}, []string{"$userRef: String"}},
		{"RemoveAppMemberInstructed", gen.RemoveAppMemberInstructed_Operation, []string{"removeAppMember(appRef: $appRef", "$userRef: ID!"}, []string{"$userRef: String"}},
		{"RemoveOrgMember", gen.RemoveOrgMember_Operation, []string{"removeOrgMember(orgRef: $orgRef", "$userRef: ID!"}, []string{"$userRef: String"}},
		{"UpdateOrgMember", gen.UpdateOrgMember_Operation, []string{"updateOrgMember(orgRef: $orgRef", "$userRef: ID!"}, []string{"$userRef: String"}},
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
