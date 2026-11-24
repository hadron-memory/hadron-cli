package api

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2997: the tests that need "a connection that never opened" now dial http://127.0.0.1:0, which no
// listener can ever answer. What a dial to port 0 fails with is platform-dependent (ECONNREFUSED on Linux;
// EADDRNOTAVAIL on macOS and Windows), so the claim the tests rely on is pinned here without depending on the
// platform running them: EVERY way a dial can fail is a transport failure the CLI reports as "could not reach the
// server" (exit 7), and the request was provably never sent, so a write's outcome is not "unknown".
func TestEveryDialFailureIsUnreachableAndNotProcessed_2997(t *testing.T) {
	for name, errno := range map[string]syscall.Errno{
		"refused (Linux, port 0)":                syscall.ECONNREFUSED,
		"address not available (macOS, Windows)": syscall.EADDRNOTAVAIL,
		"network unreachable":                    syscall.ENETUNREACH,
		"host unreachable":                       syscall.EHOSTUNREACH,
	} {
		t.Run(name, func(t *testing.T) {
			dial := &url.Error{Op: "Post", URL: "http://127.0.0.1:0/graphql", Err: &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}}
			f, ok := classifyTransport(dial)
			if !ok {
				t.Fatalf("a failed dial (%v) must classify as a transport failure", errno)
			}
			if f.what != "could not reach the server" {
				t.Errorf("a failed dial (%v) is %q, want %q", errno, f.what, "could not reach the server")
			}
			if code := exitcode.FromError(MapError(dial)); code != exitcode.Unavailable {
				t.Errorf("a failed dial (%v) exits %d, want %d", errno, code, exitcode.Unavailable)
			}
			if !RequestNotProcessed(dial) {
				t.Errorf("a failed dial (%v) is a request that was never sent: RequestNotProcessed must say so, or a write would be reported as an unknown outcome", errno)
			}
			// And the same error behind a wrapper, as the CLI returns it.
			if wrapped := fmt.Errorf("asking: %w", dial); !RequestNotProcessed(wrapped) {
				t.Errorf("a wrapped failed dial (%v) must still be never-sent", errno)
			}
		})
	}
}
