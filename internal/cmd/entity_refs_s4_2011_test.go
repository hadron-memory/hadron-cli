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
//
// want entries are anchored fragments — the call prefix, one entry per renamed
// argument at the call site, one per variable declaration — so a check never
// depends on argument order and never matches a response-selection field of
// the same name. gone entries name each dropped spelling in both its forms,
// the variable ($orgId, $orgRef: String) and the literal argument (orgId:),
// never the bare stem: a response selection may legitimately select one
// (removeMemoryMember returns memoryId/userId fields). The document-wide
// sweep against the committed snapshot lives in internal/api
// (document_ref_args_test.go); this table additionally pins, per operation,
// the exact spelling and typing the CLI sends.

// Dropped spellings by operation family, from the S4 renames (server#4219).
var (
	goneOrgId  = []string{"$orgId", "orgId:"}
	goneMemId  = []string{"$memoryId", "memoryId:"}
	goneAppId  = []string{"$appId", "appId:"}
	goneAgent  = []string{"$agentId", "agentId:"}
	goneUserId = []string{"$userId", "userId:"}
	// The collapsed pairs were optional Strings before S4; a stale optional
	// declaration sitting NEXT TO the correct one is the leftover shape the
	// old, less-anchored gone-lists could miss (cli#2679 red/green).
	goneOrgRefStr  = []string{"$orgRef: String"}
	goneAppRefStr  = []string{"$appRef: String"}
	goneUserRefStr = []string{"$userRef: String"}
	// deleteMemory/deleteOrganization & co renamed id -> ref outright.
	goneIdArg = []string{"id: $id", "$id: ID"}
)

func TestEntityRefsS4OperationsUseRenamedArgs2011(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		want     []string
		gone     []string
	}{
		{"JoinApp", gen.JoinApp_Operation,
			[]string{"joinApp(", "appRef: $appRef", "$appRef: ID!"}, goneAppId},
		{"LeaveApp", gen.LeaveApp_Operation,
			[]string{"leaveApp(", "appRef: $appRef", "$appRef: ID!"}, goneAppId},
		{"LeaveAppInstructed", gen.LeaveAppInstructed_Operation,
			[]string{"leaveApp(", "appRef: $appRef", "$appRef: ID!"}, goneAppId},
		{"CreateRevision", gen.CreateRevision_Operation,
			[]string{"createRevision(", "agentRef: $agentRef", "$agentRef: ID!"}, goneAgent},
		{"PublishRevision", gen.PublishRevision_Operation,
			[]string{"publishRevision(", "agentRef: $agentRef", "$agentRef: ID!"}, goneAgent},
		{"RestoreRevision", gen.RestoreRevision_Operation,
			[]string{"restoreRevision(", "agentRef: $agentRef", "$agentRef: ID!"}, goneAgent},
		{"AcquireEditorLock", gen.AcquireEditorLock_Operation,
			[]string{"acquireEditorLock(", "agentRef: $agentRef", "$agentRef: ID!"}, goneAgent},
		{"ReleaseEditorLock", gen.ReleaseEditorLock_Operation,
			[]string{"releaseEditorLock(", "agentRef: $agentRef", "$agentRef: ID!"}, goneAgent},
		{"DeleteMemory", gen.DeleteMemory_Operation,
			[]string{"deleteMemory(", "ref: $ref", "$ref: ID!"}, goneIdArg},
		{"SyncMemory", gen.SyncMemory_Operation,
			[]string{"syncMemory(", "ref: $ref", "$ref: ID!"}, goneIdArg},
		{"SetMemorySourceToken", gen.SetMemorySourceToken_Operation,
			[]string{"setMemorySourceToken(", "ref: $ref", "$ref: ID!"}, goneIdArg},
		{"EncryptMemory", gen.EncryptMemory_Operation,
			[]string{"encryptMemory(", "memoryRef: $memoryRef", "$memoryRef: ID!"}, goneMemId},
		{"LinkMemoryToUser", gen.LinkMemoryToUser_Operation,
			[]string{"linkMemoryToUser(", "memoryRef: $memoryRef", "$memoryRef: ID!"}, goneMemId},
		{"CreateMemorySubscription", gen.CreateMemorySubscription_Operation,
			[]string{"createMemorySubscription(", "memoryRef: $memoryRef", "orgRef: $orgRef", "$memoryRef: ID!", "$orgRef: ID!"},
			append(append([]string{}, goneMemId...), goneOrgId...)},
		{"UpdateMemorySubscription", gen.UpdateMemorySubscription_Operation,
			[]string{"updateMemorySubscription(", "memoryRef: $memoryRef", "orgRef: $orgRef", "$memoryRef: ID!", "$orgRef: ID!"},
			append(append([]string{}, goneMemId...), goneOrgId...)},
		{"DeleteMemorySubscription", gen.DeleteMemorySubscription_Operation,
			[]string{"deleteMemorySubscription(", "memoryRef: $memoryRef", "orgRef: $orgRef", "$memoryRef: ID!", "$orgRef: ID!"},
			append(append([]string{}, goneMemId...), goneOrgId...)},
		{"DeleteOrganization", gen.DeleteOrganization_Operation,
			[]string{"deleteOrganization(", "ref: $ref", "$ref: ID!"}, goneIdArg},
		{"CreateUserInvitation", gen.CreateUserInvitation_Operation,
			[]string{"createUserInvitation(", "orgRef: $orgRef", "$orgRef: ID!"}, goneOrgId},
		// createMemory's orgRef is genuinely optional ($orgRef: ID), so its
		// declaration entry pins the nullable spelling on purpose.
		{"CreateMemory", gen.CreateMemory_Operation,
			[]string{"createMemory(", "orgRef: $orgRef", "$orgRef: ID"}, goneOrgId},
		// The draft variant is the same field with draftCorpus: true.
		{"CreateMemoryDraft", gen.CreateMemoryDraft_Operation,
			[]string{"createMemory(", "draftCorpus: true", "orgRef: $orgRef", "$orgRef: ID"}, goneOrgId},
		{"LinkGithubApp", gen.LinkGithubApp_Operation,
			[]string{"linkGithubApp(", "orgRef: $orgRef", "$orgRef: ID!"}, goneOrgId},
		{"UnlinkGithubApp", gen.UnlinkGithubApp_Operation,
			[]string{"unlinkGithubApp(", "orgRef: $orgRef", "$orgRef: ID!"}, goneOrgId},
		{"UpdateOrgGithubApp", gen.UpdateOrgGithubApp_Operation,
			[]string{"updateOrgGithubApp(", "orgRef: $orgRef", "$orgRef: ID!"}, goneOrgId},
		{"AddOrgMember", gen.AddOrgMember_Operation,
			[]string{"addOrgMember(", "orgRef: $orgRef", "userRef: $userRef", "$orgRef: ID!", "$userRef: ID!"},
			append(append(append(append([]string{}, goneOrgId...), goneUserId...), goneOrgRefStr...), goneUserRefStr...)},
		{"EnsureAppMember", gen.EnsureAppMember_Operation,
			[]string{"ensureAppMember(", "appRef: $appRef", "userRef: $userRef", "$appRef: ID!", "$userRef: ID!"},
			append(append(append(append([]string{}, goneAppId...), goneUserId...), goneAppRefStr...), goneUserRefStr...)},
		{"EnsureAppMemberInstructed", gen.EnsureAppMemberInstructed_Operation,
			[]string{"ensureAppMember(", "appRef: $appRef", "userRef: $userRef", "$appRef: ID!", "$userRef: ID!"},
			append(append(append(append([]string{}, goneAppId...), goneUserId...), goneAppRefStr...), goneUserRefStr...)},
		{"RemoveAppMember", gen.RemoveAppMember_Operation,
			[]string{"removeAppMember(", "appRef: $appRef", "userRef: $userRef", "$appRef: ID!", "$userRef: ID!"},
			append(append(append(append([]string{}, goneAppId...), goneUserId...), goneAppRefStr...), goneUserRefStr...)},
		{"RemoveAppMemberInstructed", gen.RemoveAppMemberInstructed_Operation,
			[]string{"removeAppMember(", "appRef: $appRef", "userRef: $userRef", "$appRef: ID!", "$userRef: ID!"},
			append(append(append(append([]string{}, goneAppId...), goneUserId...), goneAppRefStr...), goneUserRefStr...)},
		{"RemoveOrgMember", gen.RemoveOrgMember_Operation,
			[]string{"removeOrgMember(", "orgRef: $orgRef", "userRef: $userRef", "$orgRef: ID!", "$userRef: ID!"},
			append(append(append(append([]string{}, goneOrgId...), goneUserId...), goneOrgRefStr...), goneUserRefStr...)},
		{"UpdateOrgMember", gen.UpdateOrgMember_Operation,
			[]string{"updateOrgMember(", "orgRef: $orgRef", "userRef: $userRef", "$orgRef: ID!", "$userRef: ID!"},
			append(append(append(append([]string{}, goneOrgId...), goneUserId...), goneOrgRefStr...), goneUserRefStr...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, w := range tc.want {
				if !strings.Contains(tc.op, w) {
					t.Errorf("document lacks %q:\n%s", w, tc.op)
				}
			}
			for _, g := range tc.gone {
				if strings.Contains(tc.op, g) {
					t.Errorf("document still names the dropped %q", g)
				}
			}
		})
	}
}
