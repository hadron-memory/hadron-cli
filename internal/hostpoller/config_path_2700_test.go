package hostpoller

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The helper, not the caller, classifies these paths through recording native
// function stand-ins. Their Windows subset covers only these non-absolute
// forms; it does not emulate a Windows filesystem or native Abs implementation.
func TestNonAbsoluteConfigPath2700(t *testing.T) {
	const nativeResult = "native-resolution-result"
	for _, tc := range []struct {
		name, path string
		native     bool
		want       string
	}{
		{"ordinary-relative", `host.yaml`, false, `C:\wd/host.yaml`},
		{"relative-parent-preserved", `link\..\host.yaml`, false, `C:\wd/link\..\host.yaml`},
		{"drive-rooted-backslash", `\host.yaml`, true, nativeResult},
		{"drive-rooted-slash", `/host.yaml`, true, nativeResult},
		{"drive-relative", `C:host.yaml`, true, nativeResult},
		{"other-drive-relative", `D:host.yaml`, true, nativeResult},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wdCalls, absCalls, volumeCalls := 0, 0, 0
			got := nonAbsoluteConfigPath(tc.path, func(path string) string {
				volumeCalls++
				if path != tc.path {
					t.Fatalf("classification got %q", path)
				}
				return windowsVolume2700(path)
			}, windowsSeparator2700, func() (string, error) { wdCalls++; return `C:\wd`, nil }, func(path string) (string, error) {
				absCalls++
				if path != tc.path {
					t.Fatalf("native resolver got %q", path)
				}
				return nativeResult, nil
			})
			if volumeCalls != 1 {
				t.Fatalf("volume classification calls=%d", volumeCalls)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if tc.native && (absCalls != 1 || wdCalls != 0) || !tc.native && (absCalls != 0 || wdCalls != 1) {
				t.Fatalf("native=%v abs calls=%d getwd calls=%d", tc.native, absCalls, wdCalls)
			}
		})
	}
}

func TestNonAbsoluteConfigPath2700ResolutionFailure(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		native     bool
	}{
		{"getwd", "host.yaml", false},
		{"drive-abs", "C:host.yaml", true},
		{"rooted-backslash-abs", `\host.yaml`, true},
		{"rooted-slash-abs", "/host.yaml", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wdCalls, absCalls := 0, 0
			got := nonAbsoluteConfigPath(tc.path, windowsVolume2700, windowsSeparator2700, func() (string, error) {
				wdCalls++
				if tc.native {
					t.Fatal("native failure must not fall back to slash-join")
				}
				return "", errors.New("no cwd")
			}, func(path string) (string, error) {
				absCalls++
				if !tc.native {
					t.Fatal("ordinary relative path must not use native Abs")
				}
				if path != tc.path {
					t.Fatalf("Abs got %q", path)
				}
				return "", errors.New("no absolute path")
			})
			if got != tc.path {
				t.Fatalf("failed resolution changed path: %q want %q", got, tc.path)
			}
			if tc.native && (absCalls != 1 || wdCalls != 0) || !tc.native && (absCalls != 0 || wdCalls != 1) {
				t.Fatalf("native=%v abs=%d getwd=%d", tc.native, absCalls, wdCalls)
			}
		})
	}
}

func windowsVolume2700(path string) string {
	if len(path) >= 2 && path[1] == ':' {
		return path[:2]
	}
	return ""
}
func windowsSeparator2700(c uint8) bool { return c == '/' || c == '\\' }

func TestNonAbsoluteConfigPath2700UnixBackslash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix filename semantics")
	}
	path := `\link/../host.yaml`
	got := nonAbsoluteConfigPath(path, filepath.VolumeName, os.IsPathSeparator, func() (string, error) { return "/cwd", nil }, func(string) (string, error) { t.Fatal("Unix backslash filename is not rooted"); return "", nil })
	if want := "/cwd/" + path; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
