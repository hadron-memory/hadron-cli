package coding

import (
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// routeModel is who owns a preflight route's edge (cli#1025, Holger's ruling on
// hadron-server#1797).
//
//   - mirrored: the router owns the outgoing edge and the target carries a
//     back-edge with the same label (the documented model; memories such as
//     mm-app use it). It stays the DEFAULT, so nothing changes unasked.
//   - leaf: the more specific node (the leaf) owns ONE edge to the router or to
//     an area parent, labelled `to <action>`. Nothing is written on the
//     router's outgoing set.
type routeModel string

const (
	modelMirrored routeModel = "mirrored"
	modelLeaf     routeModel = "leaf"
)

// routeModelConfigKey is the key in .hadron/config.json, the repo-local file
// the rest of this group already reads (`memory`, `coding.memory`).
const routeModelConfigKey = "preflightRouteModel"

func parseRouteModel(v, source string) (routeModel, error) {
	switch m := routeModel(strings.ToLower(strings.TrimSpace(v))); m {
	case modelMirrored, modelLeaf:
		return m, nil
	}
	return "", exitcode.Newf(exitcode.Usage, "%s takes leaf or mirrored, got %q", source, v)
}

// addRouteModelFlag registers --route-model on a preflight subcommand.
func addRouteModelFlag(cmd *cobra.Command, dst *string) {
	cmd.Flags().StringVar(dst, "route-model", "",
		"who owns the route edge: leaf (the node owns one edge to the router) or mirrored (router edge + back-edge); default: "+
			routeModelConfigKey+" in .hadron/config.json, else mirrored")
}

// resolveRouteModel is flag > .hadron/config.json > mirrored. A config file
// that exists but does not parse is refused rather than skipped: silently
// falling back to the default would write the model the repository was trying
// to move off.
func resolveRouteModel(cmd *cobra.Command, flagValue string) (routeModel, error) {
	if cmd.Flags().Changed("route-model") {
		return parseRouteModel(flagValue, "--route-model")
	}
	cfg := loadProjectCodingConfig()
	if cfg.Err != nil {
		return "", exitcode.Newf(exitcode.Usage, "%s does not parse, so %s cannot be read: %v", cfg.Path, routeModelConfigKey, cfg.Err)
	}
	if cfg.RouteModel != "" {
		return parseRouteModel(cfg.RouteModel, routeModelConfigKey+" in .hadron/config.json")
	}
	return modelMirrored, nil
}

// reWikilink matches a same-memory [[loc]] reference, ignoring any |alias or
// #heading suffix.
var reWikilink = regexp.MustCompile(`\[\[([^\]|#]+)(?:[|#][^\]]*)?\]\]`)

// bodyWikilinks is the set of locs a body links to with [[…]].
func bodyWikilinks(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range reWikilink.FindAllStringSubmatch(body, -1) {
		loc := strings.TrimSpace(m[1])
		if loc != "" && !seen[loc] {
			seen[loc] = true
			out = append(out, loc)
		}
	}
	return out
}
