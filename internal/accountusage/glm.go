package accountusage

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// GLMQuotaRel is where the glm session's status-line snippet writes the Z.ai
// quota reading, relative to the account's config dir (cli#3312, Holger's
// option B on cli#3274). collect only READS this file: it holds no key, makes
// no network call and reads no env var or key file for a glm account.
const GLMQuotaRel = "hadron-usage/glm-quota.json"

const (
	maxGLMQuotaBytes = 64 << 10
	maxGLMLimits     = 32
)

// glmWindowMinutes is the CONFIRMED list of Z.ai (unit, number) window codes
// and their length in minutes. The codes are undocumented by Z.ai, so an entry
// is added only when confirmed (console observation or a ruling), never
// guessed; a limit whose code is not here is dropped. Confirmed:
//
//	unit 3, number 5 -> 300 min (the 5-hour Coding Plan window): the reading of
//	Z.ai's own FAQ ("5-hour quota") and of the third-party parsers
//	(CodexBar zai.js: unit 3 = hours).
//
// NOT confirmed, so absent: unit 6, number 1 (weekly per CodexBar/Z.ai FAQ,
// "monthly" per one reporter) — see the PR for the open question.
var glmWindowMinutes = map[[2]int]int{
	{3, 5}: 300,
}

// glmLimitTypes are the quota types that carry a Coding Plan window. Z.ai's
// credit migration added CREDIT_LIMIT beside TOKENS_LIMIT; TIME_LIMIT is the
// separate MCP lane, not a Coding Plan window, and is dropped like any
// unknown type.
var glmLimitTypes = map[string]bool{"TOKENS_LIMIT": true, "CREDIT_LIMIT": true}

// GLMSnapshot is a validated glm-quota.json. Present is false when no file
// exists (no local source). A present file with no usable window has nil
// Primary and Secondary: "no reading yet", never zero.
type GLMSnapshot struct {
	Present    bool
	ObservedAt time.Time
	Primary    *CodexWindow // shortest window
	Secondary  *CodexWindow // next-longest, when a second confirmed window exists
}

type rawGLMLimit struct {
	Type          string   `json:"type"`
	Unit          *int     `json:"unit"`
	Number        *int     `json:"number"`
	Percentage    *float64 `json:"percentage"`
	NextResetTime *float64 `json:"nextResetTime"` // epoch milliseconds
}

type rawGLMQuota struct {
	ObservedAt *float64      `json:"observedAt"`
	Limits     []rawGLMLimit `json:"limits"`
}

// ReadGLM reads one glm account's quota file. A file that does not exist is no
// source (not an error); anything unreadable or malformed is refused, naming
// the path and a reason class, never contents.
func ReadGLM(a Account) (GLMSnapshot, error) {
	path := filepath.Join(a.ConfigDir, GLMQuotaRel)
	raw, err := readRegular(path, maxGLMQuotaBytes, "glm quota file")
	if errors.Is(err, os.ErrNotExist) {
		return GLMSnapshot{}, nil
	}
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return GLMSnapshot{}, err
		}
		return GLMSnapshot{}, bad("glm quota file %s cannot be read", path)
	}
	var q rawGLMQuota
	if err := json.Unmarshal(raw, &q); err != nil {
		return GLMSnapshot{}, bad("glm quota file %s is not valid JSON of the documented shape; its contents are not echoed", path)
	}
	if q.ObservedAt == nil {
		return GLMSnapshot{}, bad("glm quota file %s has no observedAt", path)
	}
	obs, ok := unixSeconds(*q.ObservedAt)
	if !ok {
		return GLMSnapshot{}, bad("glm quota file %s: observedAt is not a Unix time in seconds", path)
	}
	if len(q.Limits) > maxGLMLimits {
		return GLMSnapshot{}, bad("glm quota file %s lists more than %d limits", path, maxGLMLimits)
	}
	// One window per length: when two usable limits share a length (a
	// TOKENS_LIMIT and a CREDIT_LIMIT), the more-used one stands, since the
	// question collect answers is how close the account is to running out.
	byLen := map[int]*CodexWindow{}
	for _, l := range q.Limits {
		if !glmLimitTypes[l.Type] || l.Unit == nil || l.Number == nil {
			continue
		}
		mins, confirmed := glmWindowMinutes[[2]int{*l.Unit, *l.Number}]
		if !confirmed {
			continue // an unconfirmed code is dropped, never guessed
		}
		if l.Percentage == nil || math.IsNaN(*l.Percentage) || *l.Percentage < 0 || *l.Percentage > 100 {
			return GLMSnapshot{}, bad("glm quota file %s: a limit's percentage is missing or outside 0-100", path)
		}
		if l.NextResetTime == nil {
			return GLMSnapshot{}, bad("glm quota file %s: a limit has no nextResetTime", path)
		}
		// Milliseconds: a value below 1e12 (before 2001) is a seconds value
		// someone wrote by mistake, refused rather than read as 1970.
		reset, ok := unixSeconds(math.Trunc(*l.NextResetTime / 1000))
		if !ok || *l.NextResetTime != math.Trunc(*l.NextResetTime) || *l.NextResetTime < 1e12 {
			return GLMSnapshot{}, bad("glm quota file %s: nextResetTime is not a Unix time in milliseconds", path)
		}
		w := &CodexWindow{UsedPct: *l.Percentage, Minutes: mins, ResetsAt: reset}
		if cur := byLen[mins]; cur == nil || w.UsedPct > cur.UsedPct {
			byLen[mins] = w
		}
	}
	lens := make([]int, 0, len(byLen))
	for m := range byLen {
		lens = append(lens, m)
	}
	sort.Ints(lens)
	s := GLMSnapshot{Present: true, ObservedAt: obs}
	if len(lens) > 0 {
		s.Primary = byLen[lens[0]]
	}
	if len(lens) > 1 {
		s.Secondary = byLen[lens[1]]
	}
	return s, nil
}
