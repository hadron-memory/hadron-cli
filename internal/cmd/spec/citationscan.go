package spec

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Scanning SOURCE for spec citations — the population `spec lint` cannot see.
//
// The add-spec workflow tells authors to point at a spec from the code it
// governs (`// Spec: <citation>`), so the CLI creates citations outside the
// graph and nothing verified them. This is the reader for that population; the
// resolver lives in citations.go.
//
// The matcher is anchored on the prescribed `Spec:` prefix rather than on a
// comment syntax, because the live pointers use `//`, ` * ` inside a block
// comment, and a Go raw string. Anchoring on the convention keeps it
// language-agnostic, which is the argument for this living in the CLI rather
// than in each consumer repo.

var (
	// reSpecAnchor finds the prescribed pointer prefix. It selects the LINE;
	// the citations are then taken from the rest of it (see scanLine).
	reSpecAnchor = regexp.MustCompile(`(?i)\bspec:`)

	// reCitationToken matches a citation-shaped token. The leading group keeps
	// it from matching inside a longer identifier or a URL path — Go's RE2 has
	// no lookbehind, so the boundary is captured and discarded.
	//
	// A feature segment (3 digits) is REQUIRED, in BOTH modes. Making it
	// optional looked harmless — `// Spec: cor:api` is a legal pointer — but
	// ParseCitation accepts a lone 3-letter code as a flat module citation, so
	// every three-letter word on the line became a "citation":
	// `// Spec: cor:api:130 (surface contract)` yielded cor:api:130, `sur` and
	// `con`. Caught by the first test run.
	//
	// The cost is that a module- or product-level pointer is not checked. It is
	// the right trade: no live pointer is written that way, and `spec supersede`
	// only retires a numbered rule or flow, so the retirement class — the reason
	// this command exists — cannot apply to a module root anyway.
	reCitationToken = regexp.MustCompile(`(^|[^0-9A-Za-z_:/.-])([a-z]{3}(?::[a-z]{3})?:[0-9]{3}(?::[0-9]{2}){0,2})`)
)

// Scanner limits. A file bigger than maxFileBytes, or one carrying a NUL byte
// in its first sniffBytes, is generated/binary for this purpose and skipped.
const (
	maxFileBytes = 2 << 20 // 2 MiB
	sniffBytes   = 8 << 10
)

// skipDirs are never descended into. Build output and dependency trees carry
// COPIES of citations that are not pointers anyone maintains — the first live
// run drowned in hadron-server/src/generated/prisma/*, where the Prisma client
// embeds the schema comments verbatim (12 of 26 findings, every one a
// duplicate of a real pointer elsewhere).
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, ".venv": true, "venv": true,
	"__pycache__": true, ".next": true, ".svelte-kit": true,
	"coverage": true, "bin": true, "generated": true,
}

// citationRef is one occurrence of a citation in source. The same citation
// appearing in ten places yields ten refs and exactly one resolve.
type citationRef struct {
	Citation string // canonical, as ParseCitation re-emits it
	File     string
	Line     int
	Text     string // the source line, trimmed, for the finding's context
}

// scanOptions configures a source sweep.
type scanOptions struct {
	Roots   []string // files or directories; "." when the caller passes none
	Loose   bool     // match citation-shaped tokens anywhere, not just after `Spec:`
	Exclude []string // globs, matched against the base name and the root-relative path
}

// scanResult carries what was checked as well as what was found: a run that
// matched nothing must be able to say "I scanned 400 files and found no
// citations" rather than printing a checkmark that reads like a pass.
type scanResult struct {
	Refs  []citationRef
	Files int // files actually read
}

// scanCitations walks the roots and returns every citation occurrence, sorted
// by file then line.
func scanCitations(opts scanOptions) (scanResult, error) {
	roots := opts.Roots
	if len(roots) == 0 {
		roots = []string{"."}
	}
	res := scanResult{Refs: []citationRef{}}
	seenFile := map[string]bool{} // overlapping roots must not double-count
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			return scanResult{}, err
		}
		if !info.IsDir() {
			if seenFile[root] {
				continue
			}
			seenFile[root] = true
			// An explicitly named file is scanned even if its directory would
			// have been skipped — the caller pointed at it.
			refs, read, ferr := scanFile(root, root, opts)
			if ferr != nil {
				return scanResult{}, ferr
			}
			if read {
				res.Files++
			}
			res.Refs = append(res.Refs, refs...)
			continue
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = path
			}
			if d.IsDir() {
				if path == root {
					return nil
				}
				if skipDirs[d.Name()] || excluded(d.Name(), rel, opts.Exclude) {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() { // symlinks, sockets, devices
				return nil
			}
			if excluded(d.Name(), rel, opts.Exclude) || seenFile[path] {
				return nil
			}
			seenFile[path] = true
			refs, read, ferr := scanFile(path, path, opts)
			if ferr != nil {
				return ferr
			}
			if read {
				res.Files++
			}
			res.Refs = append(res.Refs, refs...)
			return nil
		})
		if err != nil {
			return scanResult{}, err
		}
	}
	sort.SliceStable(res.Refs, func(i, j int) bool {
		if res.Refs[i].File != res.Refs[j].File {
			return res.Refs[i].File < res.Refs[j].File
		}
		if res.Refs[i].Line != res.Refs[j].Line {
			return res.Refs[i].Line < res.Refs[j].Line
		}
		return res.Refs[i].Citation < res.Refs[j].Citation
	})
	return res, nil
}

// excluded reports whether a name/path matches any --exclude glob. Both
// spellings are tried so `--exclude '*.md'` and `--exclude 'docs/*'` both work.
// A malformed pattern simply never matches; the flag's job is to narrow a scan,
// not to fail one.
func excluded(name, rel string, globs []string) bool {
	for _, g := range globs {
		if ok, err := filepath.Match(g, name); err == nil && ok {
			return true
		}
		if ok, err := filepath.Match(g, rel); err == nil && ok {
			return true
		}
		// A directory glob should also prune what is under it.
		if strings.HasSuffix(g, "/*") && strings.HasPrefix(rel+"/", strings.TrimSuffix(g, "*")) {
			return true
		}
	}
	return false
}

// scanFile reads one file and extracts its citations. read is false when the
// file was skipped as binary or oversized, so the caller's "files checked"
// count reflects what was actually looked at.
func scanFile(path, display string, opts scanOptions) (refs []citationRef, read bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		// A file that vanished mid-walk is not this command's problem.
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if info.Size() > maxFileBytes {
		return nil, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsPermission(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if isBinary(data) {
		return nil, false, nil
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		for _, cit := range scanLine(line, opts.Loose) {
			refs = append(refs, citationRef{
				Citation: cit, File: display, Line: i + 1, Text: strings.TrimSpace(line),
			})
		}
	}
	return refs, true, nil
}

func isBinary(data []byte) bool {
	if len(data) > sniffBytes {
		data = data[:sniffBytes]
	}
	return bytes.IndexByte(data, 0) >= 0
}

// scanLine returns every citation on one line, de-duplicated, in order.
//
// Anchored mode takes EVERY citation after the `Spec:` prefix, not just the
// first: the live pointers routinely list several
// (`// Spec: cor:api:080:01 (collide vs relocate), cor:api:080:02 (…)`), and
// checking only the first would silently leave the rest unverified — the same
// under-coverage this command exists to remove.
func scanLine(line string, loose bool) []string {
	text := line
	if !loose {
		anchor := reSpecAnchor.FindStringIndex(line)
		if anchor == nil {
			return nil
		}
		text = line[anchor[1]:]
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range reCitationToken.FindAllStringSubmatch(text, -1) {
		tok := m[2]
		c, err := ParseCitation(tok)
		if err != nil {
			continue // citation-shaped but not a citation (prose, a URL fragment)
		}
		cit := c.Format()
		if seen[cit] {
			continue
		}
		seen[cit] = true
		out = append(out, cit)
	}
	return out
}
