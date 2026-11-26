package cmd

import (
	"encoding/json"
	"strings"
)

// forEditFixture gives a node read the revision GetSpecNodeForEdit also selects,
// the one a retirement is guarded on (cli#1736).
func forEditFixture(nodeResp string) string {
	return strings.Replace(nodeResp, `{"data":{"node":{`, `{"data":{"node":{"revision":7,`, 1)
}

// retiredAnswer is retireSpec's success for the request captured in vars.
func retiredAnswer(vars json.RawMessage) string {
	var v struct {
		Input struct {
			Ref          string `json:"ref"`
			SuccessorRef string `json:"successorRef"`
		} `json:"input"`
	}
	_ = json.Unmarshal(vars, &v)
	return `{"data":{"retireSpec":{"node":{"id":"` + v.Input.Ref + `","loc":"msg:010:02","revision":8,"tags":["spec","superseded"],"abstract":"SUPERSEDED (2026-11-26) by msg:010:03 (W3); do not apply. Historical: x"},` +
		`"successorEdge":{"id":"e1","name":"superseded-by","target":{"id":"` + v.Input.SuccessorRef + `","loc":"msg:010:03","name":"msg:010:03 — W3","tags":["spec"]}}}}}`
}
