package sigpayload

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

const fixturePath = "testdata/signaturePayload.v3.fixture.json"

type fixtureCase struct {
	Name               string `json:"name"`
	NodeID             string `json:"nodeId"`
	ApprovalHash       string `json:"approvalHash"`
	RequestID          string `json:"requestId"`
	PayloadUTF8        string `json:"payloadUtf8"`
	Canonical          string `json:"canonical"`
	ChallengeHex       string `json:"challengeHex"`
	ChallengeBase64url string `json:"challengeBase64url"`
}

// The vectors come only from the approved hadron-server fixture (its own description still says DRAFT; see testdata/PROVENANCE.md) (hadron-server
// 0a6a226f, #3120). A missing file fails the test: no locally written
// expected bytes may stand in for it.
// fixtureSHA256 is the SHA-256 of the approved fixture file at hadron-server
// 0a6a226f (see testdata/PROVENANCE.md): the copy must stay byte-identical.
const fixtureSHA256 = "34f96fc8ffa4a69fe435d369c047b7001e3f0a33b4d778226b88824a1249f17e"

func loadFixture(t *testing.T) (string, []fixtureCase) {
	t.Helper()
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("the approved v3 fixture is required: %v", err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != fixtureSHA256 {
		t.Fatalf("fixture bytes changed: sha256 %s, want %s", got, fixtureSHA256)
	}
	var fx struct {
		Version string        `json:"version"`
		Cases   []fixtureCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	if len(fx.Cases) != 4 {
		t.Fatalf("fixture has %d cases, want 4", len(fx.Cases))
	}
	return fx.Version, fx.Cases
}

func TestFixtureVectorsByteForByte(t *testing.T) {
	version, cases := loadFixture(t)
	if version != VersionV3 {
		t.Fatalf("fixture version = %q", version)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := BuildV3(c.NodeID, c.ApprovalHash, c.RequestID)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.PayloadUTF8 {
				t.Errorf("payload = %q, want %q", got, c.PayloadUTF8)
			}
			if b := base64.StdEncoding.EncodeToString(got); b != c.Canonical {
				t.Errorf("canonical = %s, want %s", b, c.Canonical)
			}
			ch := Challenge(got)
			if h := hex.EncodeToString(ch[:]); h != c.ChallengeHex {
				t.Errorf("challenge hex = %s, want %s", h, c.ChallengeHex)
			}
			if b := base64.RawURLEncoding.EncodeToString(ch[:]); b != c.ChallengeBase64url {
				t.Errorf("challenge base64url = %s, want %s", b, c.ChallengeBase64url)
			}
		})
	}
}

func TestChallengesDifferAcrossRequestAndNode(t *testing.T) {
	_, cases := loadFixture(t)
	a, b, d := cases[0], cases[1], cases[3]
	pa, err := BuildV3(a.NodeID, a.ApprovalHash, a.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := BuildV3(a.NodeID, a.ApprovalHash, b.RequestID)
	pd, _ := BuildV3(d.NodeID, a.ApprovalHash, a.RequestID)
	if Challenge(pa) == Challenge(pb) {
		t.Error("a different request id must give a different challenge")
	}
	if Challenge(pa) == Challenge(pd) {
		t.Error("the same content on a different node must give a different challenge")
	}
}

func TestBuildV3RefusesMalformedInputs(t *testing.T) {
	_, cases := loadFixture(t)
	c := cases[0]
	cases3 := []struct {
		name, node, hash, req string
		field                 string
	}{
		{"empty node", "", c.ApprovalHash, c.RequestID, "nodeId"},
		{"uppercase node hex", strings.ToUpper(c.NodeID), c.ApprovalHash, c.RequestID, "nodeId"},
		{"short node", c.NodeID[:31], c.ApprovalHash, c.RequestID, "nodeId"},
		{"node with newline", c.NodeID[:31] + "\n", c.ApprovalHash, c.RequestID, "nodeId"},
		{"empty hash", c.NodeID, "", c.RequestID, "approvalHash"},
		{"uppercase hash hex", c.NodeID, strings.ToUpper(c.ApprovalHash), c.RequestID, "approvalHash"},
		{"short hash", c.NodeID, c.ApprovalHash[:63], c.RequestID, "approvalHash"},
		{"empty request", c.NodeID, c.ApprovalHash, "", "requestId"},
		{"padded request", c.NodeID, c.ApprovalHash, c.RequestID + "==", "requestId"},
		{"short request", c.NodeID, c.ApprovalHash, c.RequestID[:21], "requestId"},
		{"request with space", c.NodeID, c.ApprovalHash, c.RequestID[:21] + " ", "requestId"},
		{"request with line", c.NodeID, c.ApprovalHash, c.RequestID[:21] + "\n", "requestId"},
	}
	for _, tc := range cases3 {
		t.Run(tc.name, func(t *testing.T) {
			out, err := BuildV3(tc.node, tc.hash, tc.req)
			if err == nil || out != nil {
				t.Fatalf("got %q, %v; want a refusal", out, err)
			}
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != tc.field {
				t.Errorf("err = %v, want a FieldError for %s", err, tc.field)
			}
		})
	}
}

// The payload shape pins: CRLF, a missing final LF, uppercase hex, extra
// memory or loc lines and the v1/v2 prefixes are all different bytes, so none
// can equal a fixture vector.
func TestNearMissPayloadsAreNotTheVector(t *testing.T) {
	_, cases := loadFixture(t)
	c := cases[0]
	want, err := BuildV3(c.NodeID, c.ApprovalHash, c.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	w := string(want)
	near := map[string]string{
		"CRLF":               strings.ReplaceAll(w, "\n", "\r\n"),
		"no final LF":        strings.TrimSuffix(w, "\n"),
		"uppercase hex":      strings.Replace(w, c.ApprovalHash, strings.ToUpper(c.ApprovalHash), 1),
		"memory line":        strings.Replace(w, "request ", "memory m\nrequest ", 1),
		"loc line":           strings.Replace(w, "request ", "loc a:b\nrequest ", 1),
		"v1 prefix":          strings.Replace(w, VersionV3, "hadron-signature-v1", 1),
		"v2 prefix":          strings.Replace(w, VersionV3, "hadron-signature-v2", 1),
		"v1 without request": "hadron-signature-v1\nnode " + c.NodeID + "\napproval-hash " + c.ApprovalHash + "\n",
	}
	for name, p := range near {
		if p == w {
			t.Errorf("%s: construction did not change the bytes", name)
		}
		if Challenge([]byte(p)) == Challenge(want) {
			t.Errorf("%s: challenge equals the vector's", name)
		}
	}
	if !strings.HasSuffix(w, "\n") || strings.Contains(w, "\r") {
		t.Errorf("payload must end in LF with no CR: %q", w)
	}
}

func TestOnlyV3IsASupportedVersion(t *testing.T) {
	if err := CheckVersion(VersionV3); err != nil {
		t.Errorf("v3 refused: %v", err)
	}
	for _, v := range []string{"", "hadron-signature-v1", "hadron-signature-v2", "hadron-signature-v4", "HADRON-SIGNATURE-V3", "hadron-signature-v3 "} {
		if err := CheckVersion(v); !errors.Is(err, ErrUnsupportedVersion) {
			t.Errorf("CheckVersion(%q) = %v, want ErrUnsupportedVersion", v, err)
		}
	}
}
