package nodedoc

import (
	"fmt"
	"path/filepath"
	"strings"
)

// locToSegments splits a loc into filesystem path segments, rejecting empty,
// `.`, `..`, and any segment carrying a path separator. Those feed directory
// creation and file writes, so a malformed loc is a real hazard — a segment
// like `../escape` or `a/b` contains no `:` and isn't equal to `..`, so it
// would slip past the exact-match checks and let filepath.Join walk outside the
// output tree (`<root>/../escape.md`). Valid locs (URN grammar) never contain
// `/`, `\`, or `..` segments, so this only ever rejects hostile input; fail
// loud rather than write to the wrong path. Hardens the server's locToSegments
// guard, which checks only empty/`.`/`..`.
func locToSegments(loc string) ([]string, error) {
	parts := strings.Split(loc, ":")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, `/\`) {
			return nil, fmt.Errorf("unsafe loc %q: empty, '.', '..', or path-separator segments are not allowed", loc)
		}
	}
	return parts, nil
}

// NodeFilePath maps a loc to its on-disk markdown path under root. The empty-loc
// root node is README.md; every other node is <seg>/<seg>.md, so a node's path
// is stable whether or not it has children (children land in the sibling <seg>/
// folder). Mirrors the server's nodeFilePath.
func NodeFilePath(root, loc string) (string, error) {
	if loc == "" {
		return filepath.Join(root, "README.md"), nil
	}
	segs, err := locToSegments(loc)
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{root}, segs...)...) + ".md", nil
}
