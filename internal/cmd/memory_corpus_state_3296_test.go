package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// hadron-cli#3296: memory get neither selects nor prints a corpus state. The
// server derives it memory-wide (DRAFT while no node is minted), so an ordinary
// memory reads DRAFT too and a line built on it would be wrong for most memories;
// `spec describe` is the surface that reads it. Selecting it in the shared document
// would also make a pre-#1447 server refuse every memory get.
func TestMemoryGetShowsNoCorpusState3296(t *testing.T) {
	if strings.Contains(gen.GetMemoryDetail_Operation, "corpusState") {
		t.Error("memory get must not select corpusState")
	}
	gql, _ := captureGraphQL(t, map[string]string{"GetMemoryDetail": `{"data":{"memory":` + memoryDetailJSON3296 + `}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "get", "acme.com:kb", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "corpus") {
		t.Errorf("memory get must print no corpus line:\n%s", out.String())
	}
}

const memoryDetailJSON3296 = `{"id":"m1","urn":"acme.com::kb","name":"KB","shortDescription":null,"description":null,
	"class":"knowledge","visibility":"ORGANIZATION","organizationId":"o1","isEncrypted":false,"maxRevCount":10,
	"tags":[],"source":null,"syncStatus":"IDLE","lastSyncedAt":null,"syncError":null,"lastSyncReport":null,
	"vectorIndexEnabled":false,"schema":null,"listedOnMarketplace":false,
	"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z"}`
