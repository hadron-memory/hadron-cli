package app

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

// settingsFlags keeps flag presence separate from false, zero and empty lists.
type settingsFlags struct {
	permission, identify, expires                             string
	timeout, dormancy, ttl                                    int
	training, expire, clearExpires, clearTools, clearSurfaces bool
	tools, surfaces, optional                                 []string
}
type preparedSettings struct {
	permission                             *gen.CreateUserPermission
	identify                               *gen.IdentifyUserMethod
	timeout, dormancy, ttl                 *int
	training, expire                       *bool
	expires                                *string
	expiresJSON, tools, surfaces, optional json.RawMessage
	changed                                []string
}

func (s *settingsFlags) flags(cmd *cobra.Command, install bool) {
	f := cmd.Flags()
	f.StringVar(&s.permission, "create-user-permission", "", "whether a chatbot session may create a platform user: DENY (never), EXPLICIT (only when asked) or IMPLICIT (whenever needed)")
	f.StringVar(&s.identify, "identify-user-method", "", "how an incoming user is identified: USER_ID (by id) or SECRET (by a shared secret)")
	f.IntVar(&s.ttl, "anonymous-ttl-days", 0, "how many days an anonymous session's data lives (server validated)")
	f.StringVar(&s.expires, "expires-at", "", "expiry timestamp (ISO 8601; server validated)")
	f.BoolVar(&s.training, "training-mode", false, "mark this App's sessions as training data for the Agent's owner (an explicit =false is sent)")
	f.StringArrayVar(&s.tools, "agent-tool", nil, "Agent tool; repeat to replace the whole list")
	f.StringArrayVar(&s.surfaces, "surface", nil, "surface string; repeat to replace the whole list; descriptive, not an authorization boundary")
	if install {
		f.IntVar(&s.timeout, "session-timeout-seconds", 0, "how long a session lasts, in seconds (server validated)")
		f.StringArrayVar(&s.optional, "install-optional", nil, "optional Agent ref to install; repeatable")
	} else {
		f.IntVar(&s.dormancy, "session-dormancy-days", 0, "after how many idle days a lapsed worker session counts as dormant (server validated)")
		f.BoolVar(&s.expire, "agent-sessions-expire", false, "whether an Agent session gets a hard expiry by default (an explicit =false is sent)")
		f.BoolVar(&s.clearExpires, "clear-expires-at", false, "clear expiry (sends null)")
		f.BoolVar(&s.clearTools, "clear-agent-tools", false, "replace Agent tools with an empty list")
		f.BoolVar(&s.clearSurfaces, "clear-surfaces", false, "replace surfaces with an empty list")
	}
}

// retireAppRole refuses --role on app install and app update (cli#2515). The App's
// own role never had a defined effect, so Holger ruled it removed (cli#2497); the
// server still accepts and ignores the field for a notice period, and the CLI no
// longer sends it. A script that still passes the flag learns why, before any
// request, instead of getting cobra's bare "unknown flag".
func retireAppRole(cmd *cobra.Command) {
	cmdutil.RetireFlags(cmd, "the App's own role setting was removed (it never had a defined effect), so there is nothing to set: drop the flag. A member's role is a different setting: see \"hadron app member roles\" and \"hadron app member add --role\"",
		cmdutil.RetiredFlag{Name: "role"})
}

func (s *settingsFlags) prepare(cmd *cobra.Command) (preparedSettings, error) {
	p := preparedSettings{changed: []string{}}
	f := cmd.Flags()
	add := func(flag, field string) bool {
		if f.Changed(flag) {
			p.changed = append(p.changed, field)
			return true
		}
		return false
	}
	if add("create-user-permission", "createUserPermission") {
		v := gen.CreateUserPermission(s.permission)
		if !slices.Contains(gen.AllCreateUserPermission, v) {
			return p, exitcode.Newf(exitcode.Usage, "invalid --create-user-permission")
		}
		p.permission = &v
	}
	if add("identify-user-method", "identifyUserMethod") {
		v := gen.IdentifyUserMethod(s.identify)
		if !slices.Contains(gen.AllIdentifyUserMethod, v) {
			return p, exitcode.Newf(exitcode.Usage, "invalid --identify-user-method")
		}
		p.identify = &v
	}
	if add("anonymous-ttl-days", "anonymousTtlDays") {
		p.ttl = &s.ttl
	}
	if add("training-mode", "trainingMode") {
		p.training = &s.training
	}
	if f.Lookup("agent-sessions-expire") != nil && add("agent-sessions-expire", "agentSessionsExpire") {
		p.expire = &s.expire
	}
	if f.Lookup("session-dormancy-days") != nil && add("session-dormancy-days", "sessionDormancyDays") {
		p.dormancy = &s.dormancy
	}
	if add("expires-at", "expiresAt") {
		if strings.TrimSpace(s.expires) == "" {
			return p, exitcode.Newf(exitcode.Usage, "--expires-at must not be blank; use --clear-expires-at on app update to clear an expiry")
		}
		if s.clearExpires {
			return p, exitcode.Newf(exitcode.Usage, "--expires-at and --clear-expires-at are mutually exclusive")
		}
		p.expires = &s.expires
		p.expiresJSON = mustJSONString(s.expires)
	} else if s.clearExpires {
		p.expiresJSON = json.RawMessage("null")
		p.changed = append(p.changed, "expiresAt")
	}
	for _, item := range []struct {
		flag, field string
		values      []string
		clear       bool
		dest        *json.RawMessage
	}{{"agent-tool", "agentTools", s.tools, s.clearTools, &p.tools}, {"surface", "surfaces", s.surfaces, s.clearSurfaces, &p.surfaces}} {
		if f.Changed(item.flag) && item.clear {
			return p, exitcode.Newf(exitcode.Usage, "--%s conflicts with its clear flag", item.flag)
		}
		if f.Changed(item.flag) || item.clear {
			v := item.values
			if item.clear {
				v = []string{}
			}
			*item.dest, _ = json.Marshal(v)
			p.changed = append(p.changed, item.field)
		}
	}
	if f.Lookup("install-optional") != nil && add("install-optional", "installOptional") {
		p.optional, _ = json.Marshal(s.optional)
	}

	return p, nil
}
