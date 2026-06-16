package spec

import (
	"fmt"
	"strconv"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Numbering policy (matches the platform-specs register):
//   - Features are numbered in tens (010, 020, 030, …) to leave headroom.
//   - Rules and flows increment by one.
//   - Rule `00` is reserved for a feature's general-provisions contract and
//     is never auto-allocated.
//   - Numbers are monotonic: allocation goes strictly above the current max,
//     so a repealed (retired) number is never recycled. Gaps are fine.

// levelSpec returns the numbering step, inclusive max, and zero-padded
// width for a citation level (2=feature, 3=rule, 4=flow).
func levelSpec(level int) (step, max, width int) {
	switch level {
	case 2:
		return 10, 990, 3
	default: // rule (3) / flow (4)
		return 1, 99, 2
	}
}

// childNumbersAt returns the integer values of locs that sit exactly one
// level below parent (e.g. parent "msg:010" yields the rule numbers under
// it). Locs that don't parse or don't share parent's prefix are ignored.
func childNumbersAt(parent Citation, locs []string) []int {
	pl := parent.Level()
	var out []int
	for _, loc := range locs {
		c, err := ParseCitation(loc)
		if err != nil || c.Level() != pl+1 {
			continue
		}
		if c.Product != parent.Product || c.Module != parent.Module {
			continue
		}
		if pl >= 2 && c.Feature != parent.Feature {
			continue
		}
		if pl >= 3 && c.Rule != parent.Rule {
			continue
		}
		var seg string
		switch pl + 1 {
		case 2:
			seg = c.Feature
		case 3:
			seg = c.Rule
		case 4:
			seg = c.Flow
		}
		if n, err := strconv.Atoi(seg); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// nextNumber finds the lowest free number strictly above max(used),
// honoring the floor, the step grid, the retired overlay, and the cap.
func nextNumber(used, retired []int, floor, step, max int) (int, bool) {
	usedSet := toIntSet(used)
	retiredSet := toIntSet(retired)
	maxUsed := 0
	for _, u := range used {
		if u > maxUsed {
			maxUsed = u
		}
	}

	if step <= 1 {
		n := maxUsed + 1
		if n < 1 {
			n = 1
		}
		if n < floor {
			n = floor
		}
		for ; n <= max; n++ {
			if !usedSet[n] && !retiredSet[n] {
				return n, true
			}
		}
		return 0, false
	}

	// Step grid (features): the next multiple of step strictly above the
	// high-water mark (and the floor).
	start := maxUsed
	if floor-1 > start {
		start = floor - 1
	}
	for n := (start/step + 1) * step; n <= max; n += step {
		if !usedSet[n] && !retiredSet[n] {
			return n, true
		}
	}
	return 0, false
}

// allocateChild computes the next free child citation under parent given
// the sibling numbers already in use and a retired-number overlay. `after`
// (0 = none) raises the floor so allocation lands strictly past it.
func allocateChild(parent Citation, used, retired []int, after int) (Citation, error) {
	childLevel := parent.Level() + 1
	if childLevel > 4 {
		return Citation{}, exitcode.Newf(exitcode.Usage, "%s is already at the deepest level (flow)", parent.Format())
	}
	step, max, width := levelSpec(childLevel)
	floor := 0
	if after > 0 {
		floor = after + 1
	}
	n, ok := nextNumber(used, retired, floor, step, max)
	if !ok {
		return Citation{}, exitcode.Newf(exitcode.Usage, "number space exhausted under %s", parent.Format())
	}
	seg := fmt.Sprintf("%0*d", width, n)
	child := parent
	switch childLevel {
	case 2:
		child.Feature = seg
	case 3:
		child.Rule = seg
	case 4:
		child.Flow = seg
	}
	return child, nil
}

func toIntSet(xs []int) map[int]bool {
	s := make(map[int]bool, len(xs))
	for _, x := range xs {
		s[x] = true
	}
	return s
}
