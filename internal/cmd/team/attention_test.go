package team

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

type attentionPage = gen.TeamAttentionPageTeamAttentionPage
type attentionItem = gen.TeamAttentionPageTeamAttentionPageItemsTeamAttentionPageItem
type previewPage = gen.TeamAttentionPreviewPageTeamAttentionPreviewPage
type previewItem = gen.TeamAttentionPreviewPageTeamAttentionPreviewPageItemsTeamAttentionPageItem

func attentionStr(s string) *string { return &s }

func TestCollectAttentionDrainsBeforeReturningToken(t *testing.T) {
	first := &attentionPage{
		ScanComplete: false, NextPage: attentionStr("page-2"),
		Items: []*attentionItem{{
			Worker: "w1", WorkerName: "Ada", Channel: "c1", ChannelName: "team",
			Unread: 3, UnreadMentions: 1, FirstUnreadSeq: attentionInt(12), LastSeq: 20,
		}},
	}
	last := &attentionPage{
		ScanComplete: true, AdoptableSince: attentionStr("adopt-only-after-delivery"),
		Items: []*attentionItem{{
			Worker: "w1", WorkerName: "Ada", Channel: "c2", ChannelName: "ops",
			Unread: 1, LastSeq: 22,
		}, {
			Worker: "w2", WorkerName: "Jonas", Channel: "c1", ChannelName: "team",
			Unread: 2, LastSeq: 20,
		}},
	}
	calls := 0
	dto, err := collectAttention("app", attentionStr("old-token"), func(since, page *string) (*attentionPage, error) {
		calls++
		switch calls {
		case 1:
			if !reflect.DeepEqual(since, attentionStr("old-token")) || page != nil {
				t.Fatalf("first page args: since=%v page=%v", since, page)
			}
			return first, nil
		case 2:
			if since != nil || !reflect.DeepEqual(page, attentionStr("page-2")) {
				t.Fatalf("continuation args: since=%v page=%v", since, page)
			}
			return last, nil
		default:
			t.Fatal("unexpected extra page")
			return nil, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || dto.Token != "adopt-only-after-delivery" || len(dto.Workers) != 2 {
		t.Fatalf("incomplete aggregate: calls=%d dto=%+v", calls, dto)
	}
	if got := dto.Workers[0]; got.Worker != "w1" || !got.Live || len(got.Channels) != 2 || got.Channels[0].UnreadMentions != 1 || *got.Channels[0].FirstUnreadSeq != 12 {
		t.Fatalf("first worker lost a page or changed counts: %+v", got)
	}
}

func TestCollectAttentionNeverReturnsAnIncompleteToken(t *testing.T) {
	cases := []struct {
		name string
		page *attentionPage
	}{
		{"missing continuation", &attentionPage{}},
		{"premature token", &attentionPage{NextPage: attentionStr("next"), AdoptableSince: attentionStr("early")}},
		{"final without token", &attentionPage{ScanComplete: true}},
		{"final with continuation", &attentionPage{ScanComplete: true, NextPage: attentionStr("next"), AdoptableSince: attentionStr("token")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dto, err := collectAttention("app", nil, func(_, _ *string) (*attentionPage, error) { return tc.page, nil })
			if err == nil || dto.Token != "" || len(dto.Workers) != 0 {
				t.Fatalf("incomplete server page escaped: dto=%+v err=%v", dto, err)
			}
		})
	}
}

func TestCollectAttentionRejectsRepeatedContinuation(t *testing.T) {
	calls := 0
	_, err := collectAttention("app", nil, func(_, _ *string) (*attentionPage, error) {
		calls++
		return &attentionPage{NextPage: attentionStr("same")}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "repeated a continuation") || calls != 2 {
		t.Fatalf("repeated page must stop: calls=%d err=%v", calls, err)
	}
}

func TestCollectAttentionPreviewOnlyExposesFinalProof(t *testing.T) {
	calls := 0
	dto, err := collectAttentionPreview("app", func(page *string) (*previewPage, error) {
		calls++
		if calls == 1 {
			if page != nil {
				t.Fatalf("first page = %v", page)
			}
			return &previewPage{NextPage: attentionStr("page-2"), Items: []*previewItem{{
				Worker: "w", WorkerName: "Ada", Channel: "c", ChannelName: "team",
				Unread: 2, FirstUnreadSeq: attentionInt(42), LastSeq: 50,
			}}}, nil
		}
		if !reflect.DeepEqual(page, attentionStr("page-2")) {
			t.Fatalf("continuation = %v", page)
		}
		return &previewPage{ScanComplete: true, Proof: attentionStr("final-proof"), ExpiresAt: attentionStr("2099-01-01T00:00:00Z")}, nil
	})
	if err != nil || calls != 2 || dto.Proof != "final-proof" || len(dto.Workers) != 1 || dto.Workers[0].Channels[0].ThroughSeq != 50 || *dto.Workers[0].Channels[0].FirstUnreadSeq != 42 {
		t.Fatalf("preview lost a page or proof: calls=%d dto=%+v err=%v", calls, dto, err)
	}
	_, err = collectAttentionPreview("app", func(_ *string) (*previewPage, error) {
		return &previewPage{NextPage: attentionStr("next"), Proof: attentionStr("premature")}, nil
	})
	if err == nil {
		t.Fatal("partial preview exposed a proof")
	}
}

func attentionInt(n int) *int { return &n }
