package memory

// The memory settings createMemory / createMemoryInApp / updateMemory take
// beyond name, class and description (cli#1340, cor:api:240:02): categories,
// the data bag, the vector index and its chunking, the catalogue URLs, the
// license and the git sync. The server owns every rule about them (who may
// set them, the vector-index defaults per class, the GitHub-only source, the
// branch syntax, the chunk arithmetic); the CLI only carries the values.

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// textSetting is a clearable text setting: on a change, an empty value is sent
// as null, which the server reads as "clear".
type textSetting struct {
	flag, usage string
	value       string
}

type memorySettings struct {
	text            []*textSetting
	data, dataFile  string
	embeddingSource string
	vectorIndex     bool
	noVectorIndex   bool
	// update only
	chunkTokens, chunkOverlap int
	forceFixedSize            bool
	noForceFixedSize          bool
	ackInversionRisk          bool

	// resolved by validate, read by the argument builders: the data file is
	// read once (cli#1476 BLIND P2) and each on/off pair resolves from the
	// flags actually passed, so an explicit =false is a value, not an absence.
	dataResolved           *json.RawMessage
	vectorResolved         *bool
	forceFixedSizeResolved *bool
}

const (
	setCategory0  = "category0"
	setCategory1  = "category1"
	setCategory2  = "category2"
	setLicense    = "license"
	setIconURL    = "icon-url"
	setHeroURL    = "hero-url"
	setHomeURL    = "home-url"
	setGitSource  = "git-source"
	setReadBranch = "read-branch"
	setWriteBrnch = "write-branch"
)

// settingFlags lists every settings flag, for the refusals on the creation
// routes that take none of them.
var settingFlags = []string{
	setCategory0, setCategory1, setCategory2, setLicense, setIconURL, setHeroURL, setHomeURL,
	setGitSource, setReadBranch, setWriteBrnch, "data", "data-file", "embedding-source",
	"vector-index", "no-vector-index", "chunk-tokens", "chunk-overlap", "force-fixed-size",
	"no-force-fixed-size", "acknowledge-vector-inversion-risk",
}

// updateOnlySettings are the dials only updateMemory takes.
var updateOnlySettings = []string{"chunk-tokens", "chunk-overlap", "force-fixed-size", "no-force-fixed-size", "acknowledge-vector-inversion-risk"}

// inAppSettings are the settings createMemoryInApp takes.
var inAppSettings = map[string]bool{"data": true, "data-file": true, "embedding-source": true, "vector-index": true, "no-vector-index": true}

func (s *memorySettings) register(cmd *cobra.Command) {
	s.text = []*textSetting{
		{flag: setCategory0, usage: "first catalogue category"},
		{flag: setCategory1, usage: "second catalogue category"},
		{flag: setCategory2, usage: "third catalogue category"},
		{flag: setLicense, usage: "the content's license, e.g. CC-BY-4.0"},
		{flag: setIconURL, usage: "icon image URL"},
		{flag: setHeroURL, usage: "hero image URL"},
		{flag: setHomeURL, usage: "home page URL"},
		{flag: setGitSource, usage: "the GitHub repository the memory syncs with"},
		{flag: setReadBranch, usage: "git branch the memory reads from"},
		{flag: setWriteBrnch, usage: "git branch the memory writes to"},
	}
	for _, t := range s.text {
		cmd.Flags().StringVar(&t.value, t.flag, "", t.usage+` ("" clears it on a change)`)
	}
	cmd.Flags().StringVar(&s.data, "data", "", `the memory's free-form data as JSON ("" or "null" clears it on a change)`)
	cmd.Flags().StringVar(&s.dataFile, "data-file", "", "read --data from a file")
	cmd.Flags().StringVar(&s.embeddingSource, "embedding-source", "", "what the vector index is built from: abstract, contentChunks or both")
	cmd.Flags().BoolVar(&s.vectorIndex, "vector-index", false, "turn the vector index on")
	cmd.Flags().BoolVar(&s.noVectorIndex, "no-vector-index", false, "turn the vector index off")
	cmd.Flags().IntVar(&s.chunkTokens, "chunk-tokens", 0, "tokens per content chunk (update only)")
	cmd.Flags().IntVar(&s.chunkOverlap, "chunk-overlap", 0, "tokens two content chunks share (update only)")
	cmd.Flags().BoolVar(&s.forceFixedSize, "force-fixed-size", false, "cut content into fixed-size chunks (update only)")
	cmd.Flags().BoolVar(&s.noForceFixedSize, "no-force-fixed-size", false, "stop forcing fixed-size chunks (update only)")
	cmd.Flags().BoolVar(&s.ackInversionRisk, "acknowledge-vector-inversion-risk", false,
		"accept that an encrypted memory's vectors are stored unencrypted, to turn its vector index on (update only)")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
}

// validate refuses, before anything is sent, a contradiction, a malformed
// value, or a flag the chosen route does not take. Gated on Changed: an
// explicit empty value is a value (it clears), not an absent flag.
func (s *memorySettings) validate(cmd *cobra.Command, creating, inApp bool) error {
	changed := cmd.Flags().Changed
	var err error
	if s.vectorResolved, err = boolPair(cmd, "vector-index", s.vectorIndex, "no-vector-index", s.noVectorIndex); err != nil {
		return err
	}
	if s.forceFixedSizeResolved, err = boolPair(cmd, "force-fixed-size", s.forceFixedSize, "no-force-fixed-size", s.noForceFixedSize); err != nil {
		return err
	}
	if changed("embedding-source") {
		if _, err := s.embedding(); err != nil {
			return err
		}
	}
	if changed("data-file") && strings.TrimSpace(s.dataFile) == "" {
		return exitcode.Newf(exitcode.Usage, `--data-file is empty: name a file, or use --data "" to clear the data. Nothing was sent`)
	}
	if changed("data") || changed("data-file") {
		if s.dataResolved, err = s.dataValue(creating); err != nil {
			return err
		}
	}
	if !creating {
		return nil
	}
	for _, name := range updateOnlySettings {
		if changed(name) {
			return exitcode.Newf(exitcode.Usage, "--%s applies only when changing a memory: create it first, then run memory set <memory> --%s. Nothing was sent", name, name)
		}
	}
	for _, t := range s.text {
		if !changed(t.flag) {
			continue
		}
		if inApp {
			return exitcode.Newf(exitcode.Usage, "--%s cannot be used with --app/--agent: createMemoryInApp takes only --data, --embedding-source and --vector-index of the settings. Nothing was sent", t.flag)
		}
		if t.value == "" {
			return exitcode.Newf(exitcode.Usage, "--%s is empty: an empty value clears a setting, and a new memory has nothing to clear. Nothing was sent", t.flag)
		}
	}
	if inApp {
		for _, name := range settingFlags {
			if changed(name) && !inAppSettings[name] {
				return exitcode.Newf(exitcode.Usage, "--%s cannot be used with --app/--agent. Nothing was sent", name)
			}
		}
	}
	return nil
}

func (s *memorySettings) setting(flag string) *textSetting {
	for _, t := range s.text {
		if t.flag == flag {
			return t
		}
	}
	panic("unknown memory setting " + flag)
}

// createText is a text setting for createMemory: nil unless passed.
func (s *memorySettings) createText(cmd *cobra.Command, flag string) *string {
	if !cmd.Flags().Changed(flag) {
		return nil
	}
	v := s.setting(flag).value
	return &v
}

// updateText is a text setting for updateMemory: nil (omitted, keep) unless
// passed; an empty value is JSON null (clear); anything else the string.
func (s *memorySettings) updateText(cmd *cobra.Command, flag string) json.RawMessage {
	if !cmd.Flags().Changed(flag) {
		return nil
	}
	v := s.setting(flag).value
	if v == "" {
		return json.RawMessage("null")
	}
	b, _ := json.Marshal(v)
	return b
}

// dataValue resolves --data / --data-file. nil when neither was passed. On a
// change "" or "null" is JSON null (clear); on create an empty value is refused.
func (s *memorySettings) dataValue(creating bool) (*json.RawMessage, error) {
	raw, flag := strings.TrimSpace(s.data), "--data"
	if s.dataFile != "" {
		flag = "--data-file"
		b, err := os.ReadFile(s.dataFile)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "reading --data-file: %v", err)
		}
		raw = strings.TrimSpace(string(b))
	}
	if raw == "" || raw == "null" {
		if creating {
			return nil, exitcode.Newf(exitcode.Usage, "%s is empty: an empty value clears the data, and a new memory has nothing to clear. Nothing was sent", flag)
		}
		msg := json.RawMessage("null")
		return &msg, nil
	}
	if !json.Valid([]byte(raw)) {
		return nil, exitcode.Newf(exitcode.Usage, `%s must contain valid JSON (use "null" or "" to clear). Nothing was sent`, flag)
	}
	msg := json.RawMessage(raw)
	return &msg, nil
}

// dataArg is the data validate resolved (nil when neither flag was passed).
// validate must have run: the file is read there, once.
func (s *memorySettings) dataArg() *json.RawMessage { return s.dataResolved }

func (s *memorySettings) embedding() (*gen.EmbeddingSource, error) {
	for _, e := range gen.AllEmbeddingSource {
		if strings.EqualFold(string(e), strings.TrimSpace(s.embeddingSource)) {
			v := e
			return &v, nil
		}
	}
	return nil, exitcode.Newf(exitcode.Usage, "invalid --embedding-source %q (want abstract, contentChunks or both). Nothing was sent", s.embeddingSource)
}

func (s *memorySettings) embeddingArg(cmd *cobra.Command) *gen.EmbeddingSource {
	if !cmd.Flags().Changed("embedding-source") {
		return nil
	}
	e, _ := s.embedding()
	return e
}

// boolPair resolves an --x / --no-x pair from the flags actually passed: nil
// (omitted, keep) when neither was, otherwise the value they state, so
// --x=false means off and --no-x=false means on. A pair that disagrees with
// itself is refused before anything is sent.
func boolPair(cmd *cobra.Command, onFlag string, on bool, offFlag string, off bool) (*bool, error) {
	var got *bool
	for _, f := range []struct {
		name string
		v    bool
	}{{onFlag, on}, {offFlag, !off}} {
		if !cmd.Flags().Changed(f.name) {
			continue
		}
		if got != nil && *got != f.v {
			return nil, exitcode.Newf(exitcode.Usage, "--%s and --%s contradict each other. Nothing was sent", onFlag, offFlag)
		}
		v := f.v
		got = &v
	}
	return got, nil
}

func intArg(cmd *cobra.Command, flag string, v int) *int {
	if !cmd.Flags().Changed(flag) {
		return nil
	}
	return &v
}

// anyChanged reports whether a settings flag was passed (for "nothing to update").
func (s *memorySettings) anyChanged(cmd *cobra.Command) bool {
	for _, name := range settingFlags {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

// createArgs are the createMemory settings arguments, in the generated order.
type createSettingArgs struct {
	category0, category1, category2 *string
	data                            *json.RawMessage
	embeddingSource                 *gen.EmbeddingSource
	vectorIndexEnabled              *bool
	license, iconURL, heroURL       *string
	homeURL, source                 *string
	readBranch, writeBranch         *string
}

func (s *memorySettings) createArgs(cmd *cobra.Command) createSettingArgs {
	return createSettingArgs{
		category0: s.createText(cmd, setCategory0), category1: s.createText(cmd, setCategory1), category2: s.createText(cmd, setCategory2),
		data: s.dataArg(), embeddingSource: s.embeddingArg(cmd), vectorIndexEnabled: s.vectorResolved,
		license: s.createText(cmd, setLicense), iconURL: s.createText(cmd, setIconURL), heroURL: s.createText(cmd, setHeroURL),
		homeURL: s.createText(cmd, setHomeURL), source: s.createText(cmd, setGitSource),
		readBranch: s.createText(cmd, setReadBranch), writeBranch: s.createText(cmd, setWriteBrnch),
	}
}

// updateArgs are the updateMemory settings arguments.
type updateSettingArgs struct {
	category0, category1, category2 json.RawMessage
	license, iconURL, heroURL       json.RawMessage
	homeURL, source                 json.RawMessage
	readBranch, writeBranch         json.RawMessage
	embeddingSource                 *gen.EmbeddingSource
	vectorIndexEnabled              *bool
	ackInversionRisk                *bool
	chunkTokens, chunkOverlap       *int
	forceFixedSize                  *bool
}

func (s *memorySettings) updateArgs(cmd *cobra.Command) updateSettingArgs {
	var ack *bool
	if cmd.Flags().Changed("acknowledge-vector-inversion-risk") {
		v := s.ackInversionRisk
		ack = &v
	}
	return updateSettingArgs{
		category0: s.updateText(cmd, setCategory0), category1: s.updateText(cmd, setCategory1), category2: s.updateText(cmd, setCategory2),
		license: s.updateText(cmd, setLicense), iconURL: s.updateText(cmd, setIconURL), heroURL: s.updateText(cmd, setHeroURL),
		homeURL: s.updateText(cmd, setHomeURL), source: s.updateText(cmd, setGitSource),
		readBranch: s.updateText(cmd, setReadBranch), writeBranch: s.updateText(cmd, setWriteBrnch),
		embeddingSource: s.embeddingArg(cmd), vectorIndexEnabled: s.vectorResolved, ackInversionRisk: ack,
		chunkTokens: intArg(cmd, "chunk-tokens", s.chunkTokens), chunkOverlap: intArg(cmd, "chunk-overlap", s.chunkOverlap),
		forceFixedSize: s.forceFixedSizeResolved,
	}
}

// updateMemory sends updateMemory with the core fields and the settings.
func updateMemory(cmd *cobra.Command, client graphql.Client, id string, name, short, description *string, tags *[]string,
	visibility *gen.MemoryVisibility, data *json.RawMessage, urn *string, maxRevCount *int, schema *json.RawMessage,
	listed *bool, u updateSettingArgs) (*gen.UpdateMemoryResponse, error) {
	return gen.UpdateMemory(cmd.Context(), client, id, name, short, description, tags, visibility, data, urn, maxRevCount, schema, listed,
		u.category0, u.category1, u.category2, u.license, u.iconURL, u.heroURL, u.homeURL, u.source, u.readBranch, u.writeBranch,
		u.embeddingSource, u.vectorIndexEnabled, u.ackInversionRisk, u.chunkTokens, u.chunkOverlap, u.forceFixedSize)
}
