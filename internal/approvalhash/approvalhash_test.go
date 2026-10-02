package approvalhash

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

func TestServerFixture(t *testing.T) {
	// This is an exact copy of hadron-server's approvalHash.fixture.json at
	// c80638ec (server #1591). Every case checks bytes as well as the hash.
	var fixture struct {
		Version string `json:"version"`
		Cases   []struct {
			Name      string `json:"name"`
			Input     Fields `json:"input"`
			Canonical string `json:"canonical"`
			Hash      string `json:"hash"`
		} `json:"cases"`
	}
	data, err := os.ReadFile("testdata/approvalHash.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Version != Version {
		t.Fatalf("fixture version %q, implementation %q", fixture.Version, Version)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("server fixture has no cases")
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			want, err := base64.StdEncoding.DecodeString(tc.Canonical)
			if err != nil {
				t.Fatal(err)
			}
			got := Canonical(tc.Input)
			if string(got) != string(want) {
				t.Errorf("canonical bytes differ:\n got: %q\nwant: %q", got, want)
			}
			if hash := Hash(tc.Input); hash != tc.Hash {
				t.Errorf("hash = %s, want %s", hash, tc.Hash)
			}
		})
	}
}
