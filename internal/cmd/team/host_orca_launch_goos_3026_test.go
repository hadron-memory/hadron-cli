package team

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// cli#3026 E2: the platform rule is pinned where the host wires the launch, not only on the helper:
// a non-Linux host is refused there, before the poller is touched (nil here proves it).
func TestWireOrcaLaunchRefusesANonLinuxHost(t *testing.T) {
	cfg := &hostpoller.Config{Workers: map[string]hostpoller.WorkerConfig{"iris": {Family: "claude"}}}
	for _, goos := range []string{"darwin", "windows"} {
		err := wireOrcaLaunchFor(goos, nil, cfg)
		if err == nil || !strings.Contains(err.Error(), "needs Linux") {
			t.Errorf("%s: want the Linux refusal from the wiring, got %v", goos, err)
		}
	}
	if err := wireOrcaLaunchFor("darwin", nil, &hostpoller.Config{}); err != nil {
		t.Errorf("a config with no workers wires nothing and asks nothing of the OS: %v", err)
	}
	if err := OrcaLaunchPlatformError("darwin"); err == nil || strings.Contains(err.Error(), "the default") {
		t.Errorf("cli#3026 E3: the refusal must not call orca \"the default\": %v", err)
	}
}

// cli#3026 item 3: `host run` says each load warning once, on stderr, at start (the sweep and the
// status read reload the file every run and say nothing, so the warning does not repeat each minute).
func TestWriteHostConfigWarnings(t *testing.T) {
	var out bytes.Buffer
	cfg := &hostpoller.Config{Warnings: []string{`worker "iris": launch is ignored`, `worker "ada": launch is ignored`}}
	if err := writeHostConfigWarnings(&out, cfg); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "warning: worker \"iris\": launch is ignored\nwarning: worker \"ada\": launch is ignored\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	out.Reset()
	if err := writeHostConfigWarnings(&out, &hostpoller.Config{}); err != nil || out.Len() != 0 {
		t.Fatalf("no warnings, no output: %q %v", out.String(), err)
	}
}
