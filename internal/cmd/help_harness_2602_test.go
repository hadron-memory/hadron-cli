package cmd

import (
	"strings"
	"testing"
)

// cli#2602: the cli#2565 example harness fails loudly where it used to stay silent.

func TestHeredocFiles2565FindsADirectoryName2602(t *testing.T) {
	files, err := heredocFiles2565("  cat > cfg/ask.json <<'EOF'\n{\"title\":\"t\"}\nEOF")
	if err != nil {
		t.Fatal(err)
	}
	if body, ok := heredocBody2565(files, "cfg/ask.json"); !ok || body != `{"title":"t"}` {
		t.Errorf("cfg/ask.json not found: %q %v (files %v)", body, ok, files)
	}
	// An example path under /etc, /home or /var runs as its base name, so the
	// heredoc that writes it is found by that name.
	files, err = heredocFiles2565("cat > /etc/ask.json <<EOF\nx\nEOF")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := heredocBody2565(files, "ask.json"); !ok {
		t.Errorf("redirected heredoc not found: %v", files)
	}
	// A different directory with the same base name is a different file.
	if _, ok := heredocBody2565(map[string]string{"a/ask.json": "x"}, "b/ask.json"); ok {
		t.Error("b/ask.json matched a/ask.json")
	}
}

func TestHeredocFiles2565RefusesAnUnterminatedHeredoc2602(t *testing.T) {
	if _, err := heredocFiles2565("cat > ask.json <<'EOF'\n{}\n"); err == nil {
		t.Error("an unterminated heredoc was silently dropped")
	}
}

func TestStripRedirect2565IgnoresQuotedAngles2602(t *testing.T) {
	for in, want := range map[string]string{
		`hadron find x > out.json`:            `hadron find x`,
		`hadron find "a > b"`:                 `hadron find "a > b"`,
		`hadron find 'a > b' > out.json`:      `hadron find 'a > b'`,
		`hadron find "say \"a > b\"" --json`:  `hadron find "say \"a > b\"" --json`,
		`hadron comment add x --body "x" > o`: `hadron comment add x --body "x"`,
	} {
		if got := stripRedirect2565(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestCollect2565PagesReportsEveryMissingPage2602(t *testing.T) {
	f, _ := testFactory(t)
	pages, missing := collect2565Pages(NewRootCmd(f), []string{"no such", "question", "also gone"})
	if len(pages) != 1 || strings.Join(missing, ",") != "no such,also gone" {
		t.Errorf("pages=%d missing=%v", len(pages), missing)
	}
}

func TestUncovered2565NamesPagesWithNoExecutedExample2602(t *testing.T) {
	results := []exampleResult{
		{Page: "a", Mode: "fixture"},
		{Page: "b", Mode: "not run"},
		{Page: "d", Mode: "fixture"},
	}
	got := uncovered2565([]string{"a", "b", "c", "d", "e"}, results, map[string]string{"c": "why", "d": "stale", "gone": "stale"})
	want := []string{"b: no executed example", "e: no executed example", "d: allowlisted as having none, but it has one", "gone: allowlisted but not a listed page"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestCheckQuestionHeredoc2565Rules2602(t *testing.T) {
	const opts = `"options":[{"key":"pg","label":"Postgres"}]`
	const rec = `"recommendation":{"key":"pg","reason":"r"}`
	for name, tc := range map[string]struct {
		body string
		ask  bool
		want string // "" = accepted
	}{
		"good":                    {`{"title":"t",` + opts + `,` + rec + `}`, true, ""},
		"yesno":                   {`{"title":"t","yesno":true,"recommendation":{"key":"yes","reason":"r"}}`, true, ""},
		"free text":               {`{"title":"t"}`, true, ""},
		"amend":                   {`{"assignee":null,"specsTouched":[{"spec":"x"}]}`, false, ""},
		"unknown key":             {`{"title":"t","parts":[]}`, true, "parts"},
		"unknown nested key":      {`{"title":"t","options":[{"key":"pg","label":"P","why":"x"}],` + rec + `}`, true, "why"},
		"unknown amend ref key":   {`{"specsTouched":[{"citation":"x"}]}`, false, "citation"},
		"options and yesno":       {`{"title":"t","yesno":true,` + opts + `,` + rec + `}`, true, "options or yesno"},
		"both recommendations":    {`{"title":"t",` + opts + `,` + rec + `,"noRecommendation":{"reason":"r"}}`, true, "not both"},
		"neither recommendation":  {`{"title":"t",` + opts + `}`, true, "neither"},
		"yesno neither":           {`{"title":"t","yesno":true}`, true, "neither"},
		"free text with one":      {`{"title":"t","noRecommendation":{"reason":"r"}}`, true, "no options"},
		"amend both":              {`{` + rec + `,"noRecommendation":{"reason":"r"}}`, false, "not both"},
		"amend options and yesno": {`{"yesno":true,` + opts + `}`, false, "options or yesno"},
	} {
		got := strings.Join(checkQuestionHeredoc2565(tc.body, tc.ask), "; ")
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

func TestSchemaUnknownKeys2565Walks2602(t *testing.T) {
	got := schemaUnknownKeys2565(t, "AskQuestionInput", `{"title":"t","grant":{"anything":1},"options":[{"key":"k","Label":"x"}],"blocks":[{"nope":1}],"bogus":1}`)
	want := "blocks[0].nope,bogus,options[0].Label"
	if strings.Join(got, ",") != want {
		t.Errorf("got %v want %s", got, want)
	}
}
