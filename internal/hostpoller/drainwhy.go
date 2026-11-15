package hostpoller

// drainWhy is WHY a held group reads as held: a code, never text read from
// /proc (the drain-held events carry it, hadron-cli#2142). It lives outside the
// per-platform scan files so the watcher and its tests build everywhere; only
// Linux has the /proc oracle that produces the codes, and the platforms
// without the zombie hold only ever answer whyUnreadable.
type drainWhy string

const (
	whyLiveMember     drainWhy = "live-member"     // a member that can still run
	whyUnseenPid      drainWhy = "unseen-pid"      // a listed pid vanished before its stat read
	whyVanishedMember drainWhy = "vanished-member" // a member seen as a zombie vanished
	whyLateFork       drainWhy = "late-fork"       // a Z member seen for the first time
	whyUnreadable     drainWhy = "unreadable"      // /proc or a stat could not be read, or no oracle here
)
