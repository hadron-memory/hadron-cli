package api

import (
	"encoding/json"
	"testing"
)

func TestParseDeliveryPending(t *testing.T) {
	for _, tc := range []struct {
		name, ext string
		count     int64
		ok        bool
	}{
		{"valid", `{"hadron":{"deliveryPending":{"v":1,"count":7,"complete":true,"asOf":"x"}}}`, 7, true},
		{"zero is a real zero", `{"hadron":{"deliveryPending":{"v":1,"count":0,"complete":true}}}`, 0, true},
		{"max safe", `{"hadron":{"deliveryPending":{"v":1,"count":9007199254740991,"complete":true}}}`, 9007199254740991, true},
		{"unsafe", `{"hadron":{"deliveryPending":{"v":1,"count":9007199254740992,"complete":true}}}`, 0, false},
		{"v2", `{"hadron":{"deliveryPending":{"v":2,"count":1,"complete":true}}}`, 0, false},
		{"incomplete", `{"hadron":{"deliveryPending":{"v":1,"count":1,"complete":false}}}`, 0, false},
		{"fraction", `{"hadron":{"deliveryPending":{"v":1,"count":1.5,"complete":true}}}`, 0, false},
		{"negative", `{"hadron":{"deliveryPending":{"v":1,"count":-1,"complete":true}}}`, 0, false},
		{"string", `{"hadron":{"deliveryPending":{"v":1,"count":"1","complete":true}}}`, 0, false},
		{"absent", `{}`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ext map[string]any
			if err := json.Unmarshal([]byte(tc.ext), &ext); err != nil {
				t.Fatal(err)
			}
			n, ok := ParseDeliveryPending(ext)
			if n != tc.count || ok != tc.ok {
				t.Fatalf("got (%d,%v), want (%d,%v)", n, ok, tc.count, tc.ok)
			}
		})
	}
	var nilRec *DeliveryPendingRecorder
	nilRec.record(map[string]any{})
	if _, ok := nilRec.Count(); ok {
		t.Fatal("nil recorder must report nothing")
	}
}
