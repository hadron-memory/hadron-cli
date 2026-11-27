package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// Dropped spellings by operation family, from the S4 renames (server#4219).
var (
	s4GoneOrgId   = []string{"$orgId", "orgId:"}
	s4GoneMemId   = []string{"$memoryId", "memoryId:"}
	s4GoneAppId   = []string{"$appId", "appId:"}
	s4GoneAgentId = []string{"$agentId", "agentId:"}
	s4GoneUserId  = []string{"$userId", "userId:"}
	// The collapsed pairs were optional Strings before S4; a stale optional
	// declaration sitting NEXT TO the correct one is the leftover shape the
	// old, less-anchored gone-lists could miss (cli#2679 red/green).
	s4GoneOrgRefStr  = []string{"$orgRef: String"}
	s4GoneAppRefStr  = []string{"$appRef: String"}
	s4GoneUserRefStr = []string{"$userRef: String"}
	// deleteMemory/deleteOrganization & co renamed id -> ref outright.
	s4GoneIdArg = []string{"id: $id", "$id: ID"}
	// createMemory's orgRef stays optional ($orgRef: ID): its declaration
	// entry is the bare prefix, so the REQUIRED spelling is named here as the
	// dropped one. That pins nullability without depending on which variable
	// follows $orgRef in the document (cli#2712).
	s4GoneOrgRefRequired = []string{"$orgRef: ID!"}
)

// s4GoneLists joins gone-lists without the nested-append chains.
func s4GoneLists(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

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
// the same name. A declaration entry that pins NULLABILITY names the wrong
// spelling in gone as well: the bare type is a PREFIX of its non-null form, so
// a want entry alone is satisfied by the wrong spelling (#2696 review) —
// and anchoring it on a following comma only worked while another variable
// followed it (cli#2712).
// gone entries name each dropped spelling in both its forms, the variable
// ($orgId, $orgRef: String) and the literal argument (orgId:), never the bare
// stem: a response selection may legitimately select one (removeMemoryMember
// returns memoryId/userId fields). The document-wide sweep against the
// committed snapshot lives in internal/api (document_ref_args_test.go); this
// table additionally pins, per operation, the exact spelling and typing the
// CLI sends.
func TestEntityRefsS4OperationsUseRenamedArgs2011(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		want     []string
		gone     []string
	}{
		{"JoinApp", gen.JoinApp_Operation,
			[]string{"joinApp(", "appRef: $appRef", "$appRef: ID!"}, s4GoneAppId},
		{"LeaveApp", gen.LeaveApp_Operation,
			[]string{"leaveApp(", "appRef: $appRef", "$appRef: ID!"}, s4GoneAppId},
		{"LeaveAppInstructed", gen.LeaveAppInstructed_Operation,
			[]string{"leaveApp(", "appRef: $appRef", "$appRef: ID!"}, s4GoneAppId},
		{"CreateRevision", gen.CreateRevision_Operation,
			[]string{"createRevision(", "agentRef: $agentRef", "$agentRef: ID!"}, s4GoneAgentId},
		{"PublishRevision", gen.PublishRevision_Operation,
			[]string{"publishRevision(", "agentRef: $agentRef", "$agentRef: ID!"}, s4GoneAgentId},
		{"RestoreRevision", gen.RestoreRevision_Operation,
			[]string{"restoreRevision(", "agentRef: $agentRef", "$agentRef: ID!"}, s4GoneAgentId},
		{"AcquireEditorLock", gen.AcquireEditorLock_Operation,
			[]string{"acquireEditorLock(", "agentRef: $agentRef", "$agentRef: ID!"}, s4GoneAgentId},
		{"ReleaseEditorLock", gen.ReleaseEditorLock_Operation,
			[]string{"releaseEditorLock(", "agentRef: $agentRef", "$agentRef: ID!"}, s4GoneAgentId},
		{"DeleteMemory", gen.DeleteMemory_Operation,
			[]string{"deleteMemory(", "ref: $ref", "$ref: ID!"}, s4GoneIdArg},
		{"SyncMemory", gen.SyncMemory_Operation,
			[]string{"syncMemory(", "ref: $ref", "$ref: ID!"}, s4GoneIdArg},
		{"SetMemorySourceToken", gen.SetMemorySourceToken_Operation,
			[]string{"setMemorySourceToken(", "ref: $ref", "$ref: ID!"}, s4GoneIdArg},
		{"EncryptMemory", gen.EncryptMemory_Operation,
			[]string{"encryptMemory(", "memoryRef: $memoryRef", "$memoryRef: ID!"}, s4GoneMemId},
		{"LinkMemoryToUser", gen.LinkMemoryToUser_Operation,
			[]string{"linkMemoryToUser(", "memoryRef: $memoryRef", "$memoryRef: ID!"}, s4GoneMemId},
		{"CreateMemorySubscription", gen.CreateMemorySubscription_Operation,
			[]string{"createMemorySubscription(", "memoryRef: $memoryRef", "orgRef: $orgRef", "$memoryRef: ID!", "$orgRef: ID!"},
			s4GoneLists(s4GoneMemId, s4GoneOrgId)},
		{"UpdateMemorySubscription", gen.UpdateMemorySubscription_Operation,
			[]string{"updateMemorySubscription(", "memoryRef: $memoryRef", "orgRef: $orgRef", "$memoryRef: ID!", "$orgRef: ID!"},
			s4GoneLists(s4GoneMemId, s4GoneOrgId)},
		{"DeleteMemorySubscription", gen.DeleteMemorySubscription_Operation,
			[]string{"deleteMemorySubscription(", "memoryRef: $memoryRef", "orgRef: $orgRef", "$memoryRef: ID!", "$orgRef: ID!"},
			s4GoneLists(s4GoneMemId, s4GoneOrgId)},
		{"DeleteOrganization", gen.DeleteOrganization_Operation,
			[]string{"deleteOrganization(", "ref: $ref", "$ref: ID!"}, s4GoneIdArg},
		{"CreateUserInvitation", gen.CreateUserInvitation_Operation,
			[]string{"createUserInvitation(", "orgRef: $orgRef", "$orgRef: ID!"}, s4GoneOrgId},
		// createMemory's orgRef is genuinely optional ($orgRef: ID), so its
		// declaration entry is the bare "$orgRef: ID" — which the required
		// "$orgRef: ID!" also CONTAINS, so that spelling goes in the gone-list
		// instead. (The earlier "$orgRef: ID," anchor only matched while
		// another variable followed $orgRef.)
		{"CreateMemory", gen.CreateMemory_Operation,
			[]string{"createMemory(", "orgRef: $orgRef", "$orgRef: ID"}, s4GoneLists(s4GoneOrgId, s4GoneOrgRefRequired)},
		{"LinkGithubApp", gen.LinkGithubApp_Operation,
			[]string{"linkGithubApp(", "orgRef: $orgRef", "$orgRef: ID!"}, s4GoneOrgId},
		{"UnlinkGithubApp", gen.UnlinkGithubApp_Operation,
			[]string{"unlinkGithubApp(", "orgRef: $orgRef", "$orgRef: ID!"}, s4GoneOrgId},
		{"UpdateOrgGithubApp", gen.UpdateOrgGithubApp_Operation,
			[]string{"updateOrgGithubApp(", "orgRef: $orgRef", "$orgRef: ID!"}, s4GoneOrgId},
		{"AddOrgMember", gen.AddOrgMember_Operation,
			[]string{"addOrgMember(", "orgRef: $orgRef", "userRef: $userRef", "$orgRef: ID!", "$userRef: ID!"},
			s4GoneLists(s4GoneOrgId, s4GoneUserId, s4GoneOrgRefStr, s4GoneUserRefStr)},
		{"EnsureAppMember", gen.EnsureAppMember_Operation,
			[]string{"ensureAppMember(", "appRef: $appRef", "userRef: $userRef", "$appRef: ID!", "$userRef: ID!"},
			s4GoneLists(s4GoneAppId, s4GoneUserId, s4GoneAppRefStr, s4GoneUserRefStr)},
		{"EnsureAppMemberInstructed", gen.EnsureAppMemberInstructed_Operation,
			[]string{"ensureAppMember(", "appRef: $appRef", "userRef: $userRef", "$appRef: ID!", "$userRef: ID!"},
			s4GoneLists(s4GoneAppId, s4GoneUserId, s4GoneAppRefStr, s4GoneUserRefStr)},
		{"RemoveAppMember", gen.RemoveAppMember_Operation,
			[]string{"removeAppMember(", "appRef: $appRef", "userRef: $userRef", "$appRef: ID!", "$userRef: ID!"},
			s4GoneLists(s4GoneAppId, s4GoneUserId, s4GoneAppRefStr, s4GoneUserRefStr)},
		{"RemoveAppMemberInstructed", gen.RemoveAppMemberInstructed_Operation,
			[]string{"removeAppMember(", "appRef: $appRef", "userRef: $userRef", "$appRef: ID!", "$userRef: ID!"},
			s4GoneLists(s4GoneAppId, s4GoneUserId, s4GoneAppRefStr, s4GoneUserRefStr)},
		{"RemoveOrgMember", gen.RemoveOrgMember_Operation,
			[]string{"removeOrgMember(", "orgRef: $orgRef", "userRef: $userRef", "$orgRef: ID!", "$userRef: ID!"},
			s4GoneLists(s4GoneOrgId, s4GoneUserId, s4GoneOrgRefStr, s4GoneUserRefStr)},
		{"UpdateOrgMember", gen.UpdateOrgMember_Operation,
			[]string{"updateOrgMember(", "orgRef: $orgRef", "userRef: $userRef", "$orgRef: ID!", "$userRef: ID!"},
			s4GoneLists(s4GoneOrgId, s4GoneUserId, s4GoneOrgRefStr, s4GoneUserRefStr)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, w := range tc.want {
				if !strings.Contains(tc.op, w) {
					t.Errorf("document lacks %q:\n%s", w, tc.op)
				}
			}
			// "$orgRef: ID" is a prefix of "$orgRef: IDAlias" too, so the
			// optional declaration is also checked to end at a type boundary.
			if tc.name == "CreateMemory" {
				if !regexp.MustCompile(`\$orgRef: ID([\s,)]|$)`).MatchString(tc.op) {
					t.Errorf("$orgRef is not declared as exactly the nullable ID type:\n%s", tc.op)
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
