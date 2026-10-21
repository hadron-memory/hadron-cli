package team

import (
	"context"
	"errors"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"io"
	"strings"
	"testing"
)

func TestChatCheckDrainOutputAndOriginalHead(t *testing.T) {
	s := checkSnapshot{ChannelID: "channel", Incarnation: 3, Cursor: 10, Head: 90}
	calls := []int{}
	published := false
	marked := 0
	deps := checkDeps{
		page: func(_ context.Context, since int) ([]teamChatMessageDTO, error) {
			calls = append(calls, since)
			switch since {
			case 91:
				return []teamChatMessageDTO{{Seq: 40}, {Seq: 80}}, nil
			case 40:
				return []teamChatMessageDTO{{Seq: 20}}, nil
			default:
				return nil, nil
			}
		},
		publish: func(ms []teamChatMessageDTO) (string, error) {
			if len(ms) != 3 {
				t.Fatal(ms)
			}
			published = true
			return "/private/messages.json", nil
		},
		mark: func(context.Context) (bool, error) {
			if !published {
				t.Fatal("mark before output")
			}
			marked++
			return true, nil
		},
		readback: func(context.Context) (bool, error) { t.Fatal("unexpected readback"); return false, nil },
	}
	r, err := runChatCheck(context.Background(), s, deps)
	if err != nil || r.MessageCount != 3 || r.SampledHead != 90 || marked != 1 || len(calls) != 3 {
		t.Fatalf("%+v %v calls=%v marks=%d", r, err, calls, marked)
	}
}

func TestChatCheckPreMarkFailures(t *testing.T) {
	for _, mode := range []string{"page", "output", "cycle", "late", "duplicate", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			marks := 0
			pages := 0
			deps := checkDeps{page: func(context.Context, int) ([]teamChatMessageDTO, error) {
				pages++
				if pages > 1 {
					t.Fatal("invalid page was not refused immediately")
				}
				switch mode {
				case "page":
					return nil, errors.New("read")
				case "cycle":
					return []teamChatMessageDTO{{Seq: 10}}, nil
				case "late":
					return []teamChatMessageDTO{{Seq: 91}}, nil
				case "duplicate":
					return []teamChatMessageDTO{{Seq: 20}, {Seq: 20}}, nil
				}
				return nil, nil
			}, publish: func([]teamChatMessageDTO) (string, error) {
				if mode == "output" {
					return "", io.ErrShortWrite
				}
				if mode == "cancel" {
					cancel()
				}
				return "", nil
			}, mark: func(context.Context) (bool, error) { marks++; return true, nil }}
			inc := 2
			_, err := runChatCheck(ctx, checkSnapshot{Incarnation: inc, CursorIncarnation: &inc, Cursor: 10, Head: 90}, deps)
			if err == nil || marks != 0 {
				t.Fatalf("err=%v marks=%d", err, marks)
			}
		})
	}
}

func TestChatCheckLostReplyRecovery(t *testing.T) {
	for _, mode := range []string{"readback", "retry", "uncertain", "refused"} {
		t.Run(mode, func(t *testing.T) {
			marks := 0
			reads := 0
			deps := checkDeps{page: func(context.Context, int) ([]teamChatMessageDTO, error) { return nil, nil }, publish: func([]teamChatMessageDTO) (string, error) { return "", nil }, mark: func(context.Context) (bool, error) {
				marks++
				if mode == "refused" {
					return false, errCheckIncarnation
				}
				if marks == 2 && mode == "retry" {
					return true, nil
				}
				return false, errCheckTransport
			}, readback: func(context.Context) (bool, error) { reads++; return mode == "readback", nil }}
			r, err := runChatCheck(context.Background(), checkSnapshot{Head: 0}, deps)
			if mode == "readback" || mode == "retry" {
				if err != nil || r.MarkStatus != "confirmed" {
					t.Fatal(r, err)
				}
			} else if err == nil {
				t.Fatal("expected failure")
			}
			if marks > 2 {
				t.Fatal(marks)
			}
			if mode == "refused" && (marks != 1 || reads != 0) {
				t.Fatal(marks, reads)
			}
		})
	}
}

func TestChatCheckInterleavedOrder(t *testing.T) {
	var timeline strings.Builder
	count := 0
	d := checkDeps{
		page: func(context.Context, int) ([]teamChatMessageDTO, error) {
			count++
			if count == 1 {
				return []teamChatMessageDTO{{Seq: 1}}, nil
			}
			return nil, nil
		},
		publish: func([]teamChatMessageDTO) (string, error) {
			_, _ = timeline.WriteString("FILE_COMPLETE\nPATH_PRINTED\n")
			return "file", nil
		},
		mark: func(context.Context) (bool, error) { _, _ = timeline.WriteString("MARK\n"); return true, nil },
	}
	_, e := runChatCheck(context.Background(), checkSnapshot{Head: 1}, d)
	if e != nil {
		t.Fatal(e)
	}
	if timeline.String() != "FILE_COMPLETE\nPATH_PRINTED\nMARK\n" {
		t.Fatal(timeline.String())
	}
}

func TestChatCheckBoundaryFailuresFailFast(t *testing.T) {
	for _, mode := range []string{"equal_before", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			calls, marks := 0, 0
			inc := 2
			d := checkDeps{page: func(context.Context, int) ([]teamChatMessageDTO, error) {
				calls++
				if calls > 1 {
					t.Fatal("nonprogress was not refused on first page")
				}
				if mode == "equal_before" {
					return []teamChatMessageDTO{{Seq: 91}}, nil
				}
				return []teamChatMessageDTO{{Seq: 20}, {Seq: 20}}, nil
			}, publish: func([]teamChatMessageDTO) (string, error) { t.Fatal("invalid page reached output"); return "", nil }, mark: func(context.Context) (bool, error) { marks++; return true, nil }}
			_, e := runChatCheck(context.Background(), checkSnapshot{Incarnation: inc, CursorIncarnation: &inc, Cursor: 10, Head: 90}, d)
			if exitcode.FromError(e) != exitcode.Unavailable || marks != 0 || calls != 1 {
				t.Fatal(e, calls, marks)
			}
		})
	}
}

func TestChatCheckHeadOverflowRefusesBeforePaging(t *testing.T) {
	_, e := runChatCheck(context.Background(), checkSnapshot{Head: 2147483647}, checkDeps{page: func(context.Context, int) ([]teamChatMessageDTO, error) {
		t.Fatal("overflow reached page")
		return nil, nil
	}})
	if exitcode.FromError(e) != exitcode.Conflict {
		t.Fatal(e)
	}
}

func TestChatCheckUnvouchedMarkGuidance(t *testing.T) {
	for _, lost := range []bool{false, true} {
		marks := 0
		d := checkDeps{
			page:    func(context.Context, int) ([]teamChatMessageDTO, error) { return nil, nil },
			publish: func([]teamChatMessageDTO) (string, error) { return "", nil },
			mark: func(context.Context) (bool, error) {
				marks++
				if lost && marks == 1 {
					return false, errCheckTransport
				}
				return false, errCheckCursorLabel
			},
			readback: func(context.Context) (bool, error) { return false, nil },
		}
		r, err := runChatCheck(context.Background(), checkSnapshot{Head: 0}, d)
		if exitcode.FromError(err) != checkUncertain || r.MarkStatus != "uncertain" || r.ArtifactPath != nil || marks != func() int {
			if lost {
				return 2
			}
			return 1
		}() {
			t.Fatal(r, err, marks)
		}
		r.Error = &checkError{Code: checkUncertain, Message: err.Error()}
		var out strings.Builder
		if e := renderCheckReceipt(&out, r); e != nil {
			t.Fatal(e)
		}
		text := out.String()
		if !strings.Contains(text, "Check incomplete:") || !strings.Contains(text, "do not loop on retries") || strings.Contains(text, "artifact") || strings.Contains(text, "Check failed") || strings.Contains(text, "run team chat check again") {
			t.Fatal(text)
		}
	}
}
