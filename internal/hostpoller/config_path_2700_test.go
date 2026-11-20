package hostpoller

import (
	"errors"
	"testing"
)

// These cases supply the native Windows classification explicitly: filepath
// on the Linux test host cannot classify Windows volumes. Native resolution
// is a recording stand-in, so the table pins dispatch, not a simulated OS.
func TestNonAbsoluteConfigPath2700(t *testing.T) {
	for _, tc := range []struct {
		name, path, volume string
		rooted, native     bool
		want               string
	}{
		{"ordinary-relative", `host.yaml`, "", false, false, `C:\wd/host.yaml`},
		{"relative-parent-preserved", `link\..\host.yaml`, "", false, false, `C:\wd/link\..\host.yaml`},
		{"drive-rooted-backslash", `\host.yaml`, "", true, true, `C:\host.yaml`},
		{"drive-rooted-slash", `/host.yaml`, "", true, true, `C:\host.yaml`},
		{"drive-relative", `C:host.yaml`, "C:", false, true, `C:\drive-cwd\host.yaml`},
		{"other-drive-relative", `D:host.yaml`, "D:", false, true, `D:\drive-cwd\host.yaml`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wdCalls, absCalls := 0, 0
			got := nonAbsoluteConfigPath(tc.path, tc.volume, tc.rooted, func() (string, error) { wdCalls++; return `C:\wd`, nil }, func(path string) (string, error) {
				absCalls++
				if path != tc.path {
					t.Fatalf("native resolver got %q", path)
				}
				return tc.want, nil
			})
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
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "getwd", true: "native-abs"}[native], func(t *testing.T) {
			path, volume := "host.yaml", ""
			if native {
				path, volume = "C:host.yaml", "C:"
			}
			got := nonAbsoluteConfigPath(path, volume, false, func() (string, error) {
				if native {
					t.Fatal("native resolution failure must not fall back to slash-join")
				}
				return "", errors.New("no cwd")
			}, func(string) (string, error) { return "", errors.New("no absolute path") })
			if got != path {
				t.Fatalf("failed resolution changed path: %q want %q", got, path)
			}
		})
	}
}
