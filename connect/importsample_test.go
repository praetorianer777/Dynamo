package main

import (
	"strings"
	"testing"
)

func TestAnItemIsFoundByTheNumberInItsTitle(t *testing.T) {
	if n, ok := numberOf("Charge from the sun (#12)"); !ok || n != 12 {
		t.Fatalf("got %d %v", n, ok)
	}
	for _, title := range []string{"Charge (#12) later", "No number", "(#x)"} {
		if _, ok := numberOf(title); ok {
			t.Errorf("%q has a number", title)
		}
	}
	long := strings.Repeat("ü", 300)
	marked := markedTitle(long, 7)
	if n, ok := numberOf(marked); !ok || n != 7 || len([]rune(marked)) > 215 {
		t.Fatalf("a long title became %d runes, numbered %d %v", len([]rune(marked)), n, ok)
	}
}

func TestPlainTextBecomesArmaturesRichText(t *testing.T) {
	if richText("  \n\n ") != nil {
		t.Fatal("blank text made a description")
	}
	doc := richText("First line\r\nstill first\r\n\r\n\r\nSecond")
	paragraphs := doc["content"].([]any)
	if doc["type"] != "doc" || len(paragraphs) != 2 {
		t.Fatalf("got %v", doc)
	}
	text := paragraphs[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]
	if text != "First line\nstill first" {
		t.Fatalf("the first paragraph is %q", text)
	}
	if got := truncate(strings.Repeat("a", maxText+10), maxText); !strings.HasSuffix(got, "(cut short by the import)") {
		t.Fatal("a long text was not cut short")
	}
}

func TestTheSampleSettingsAreRead(t *testing.T) {
	env := map[string]string{"DYNAMO_SAMPLE_SOURCES": "sparkplug, evcc", "DYNAMO_SAMPLE_MAX_ISSUES": "50", "GITHUB_TOKEN": "t"}
	cfg, err := loadSample(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.snapshots) != 1 || cfg.snapshots[0] != "sparkplug" || len(cfg.sources) != 1 || cfg.sources[0].key != "EVCC" || cfg.maxIssues != 50 || cfg.token != "t" {
		t.Fatalf("read %+v", cfg)
	}
	for _, bad := range []map[string]string{
		{"DYNAMO_SAMPLE_SOURCES": ""},
		{"DYNAMO_SAMPLE_SOURCES": "sparkplug", "DYNAMO_SAMPLE_MAX_ISSUES": "-1"},
		{"DYNAMO_SAMPLE_SOURCES": "nosuch"},
	} {
		if _, err := loadSample(func(k string) string { return bad[k] }); err == nil {
			t.Errorf("%v was taken", bad)
		}
	}
}
