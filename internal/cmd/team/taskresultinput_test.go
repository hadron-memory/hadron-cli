package team

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func findingInputJSON1550(t *testing.T, id any, severity, text string) string {
	t.Helper()
	m := map[string]any{"severity": severity, "text": text}
	if id != nil {
		m["id"] = id
	}
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestParseTaskFindingsBoundaries1550(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"empty", `[]`, true},
		{"empty text", `[{"severity":"note","text":""}]`, true},
		{"id null", `[{"id":null,"severity":"P2","text":"x"}]`, true},
		{"pair", `[{"severity":"P3","text":"\ud83d\ude00"}]`, true},
		{"replacement", `[{"severity":"P1","text":"�"}]`, true},
		{"id numeric", `[{"id":1,"severity":"P1","text":"x"}]`, false},
		{"text numeric", `[{"severity":"P1","text":1}]`, false},
		{"severity missing", `[{"text":"x"}]`, false},
		{"id case distinct", `[{"id":"A","severity":"P1","text":"x"},{"id":"a","severity":"P2","text":"x"}]`, true},
		{"all ids null", `[{"id":null,"severity":"P1","text":"x"},{"severity":"note","text":"x"}]`, true},
		{"mixed null", `[{"id":null,"severity":"P1","text":"x"},{"id":"A","severity":"note","text":"x"}]`, false},
		{"root unknown", `{"SENTINEL":"x"}`, false},
		{"array trailing comma", `[{"severity":"P1","text":"x"},]`, false},
		{"object duplicate escaped key", `[{"severity":"P1","text":"x","te\u0078t":"SENTINEL"}]`, false},
		{"escaped backslash", `[{"severity":"P1","text":"\\ud800"}]`, true},
		{"high not low", `[{"severity":"P1","text":"\ud800\u0041"}]`, false},
		{"high high", `[{"severity":"P1","text":"\ud800\ud800"}]`, false},
		{"low alone", `[{"severity":"P1","text":"\udfff"}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := parseTaskFindings([]byte(tc.body))
			if (err == nil) != tc.valid {
				t.Fatalf("valid%t err%v", tc.valid, err)
			}
			if tc.valid && rows == nil {
				t.Fatal("present [] became nil")
			}
			if err != nil && strings.Contains(err.Error(), "SENTINEL") {
				t.Fatal("input leaked")
			}
		})
	}
	for _, text := range []string{strings.Repeat("a", 2000), strings.Repeat("😀", 2000), strings.Repeat("é", 1000), "  newline\n\tcontrol\x1b  "} {
		body := "[" + findingInputJSON1550(t, nil, "note", text) + "]"
		rows, err := parseTaskFindings([]byte(body))
		if err != nil || rows[0].Text != text {
			t.Fatalf("valid text lost: %v", err)
		}
	}
	for _, text := range []string{strings.Repeat("a", 2001), strings.Repeat("😀", 2001), strings.Repeat("é", 1000) + "e"} {
		if _, err := parseTaskFindings([]byte("[" + findingInputJSON1550(t, nil, "P3", text) + "]")); err == nil {
			t.Fatal("overlong code points accepted")
		}
	}
	for _, id := range []string{"A", strings.Repeat("a", 32), "_-09"} {
		if _, err := parseTaskFindings([]byte("[" + findingInputJSON1550(t, id, "P2", "x") + "]")); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"", strings.Repeat("a", 33), "a.b", "é", "a b", "a\n"} {
		if _, err := parseTaskFindings([]byte("[" + findingInputJSON1550(t, id, "P2", "x") + "]")); err == nil {
			t.Fatal("bad id accepted")
		}
	}
	for _, n := range []int{100, 101} {
		items := make([]string, n)
		for i := range items {
			items[i] = fmt.Sprintf(`{"severity":"P1","text":"%d"}`, i)
		}
		_, err := parseTaskFindings([]byte("[" + strings.Join(items, ",") + "]"))
		if (err == nil) != (n == 100) {
			t.Fatalf("count%d err%v", n, err)
		}
	}
}
