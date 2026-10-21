package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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

func TestAGitHubLabelBecomesOneWordForArmature(t *testing.T) {
	got := labelNames([]string{"good first issue", " bug ", "Good  First\tIssue", "", strings.Repeat("x", 45), "needs: decision"})
	want := []string{"good-first-issue", "bug", strings.Repeat("x", 40), "needs:-decision"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAStatementShowsItsAttachedPicturesAndLinksTheRest(t *testing.T) {
	md := "See ![the chart](https://x/a.png \"t\") and ![](https://x/b.png).\n<img width=\"300\" src=\"https://x/c.png\" alt=\"c\">"
	if got := pictureURLs(md + " ![again](https://x/a.png) ![local](docs/d.png)"); !slices.Equal(got, []string{"https://x/a.png", "https://x/b.png", "https://x/c.png"}) {
		t.Fatalf("found the pictures %q", got)
	}
	got := withPictures(md, map[string]string{"https://x/c.png": "id-c"})
	want := "See [the chart](https://x/a.png) and [image](https://x/b.png).\n![c](exciter:attachment/id-c)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAnIssueBecomesABugAStoryOrATask(t *testing.T) {
	for labels, want := range map[string]string{"bug,enhancement": "Bug", "type: bug": "Bug", "debugging": "Task", "enhancement": "Story", "docs": "Task", "": "Task"} {
		if got := (sampleIssue{Labels: strings.Split(labels, ",")}).kind(); got != want {
			t.Errorf("labels %q made a %s, want a %s", labels, got, want)
		}
	}
}

func TestARequirementGetsThePicturesOfItsIssue(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0000IHDR")
	pictures := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a.png":
			w.Write(png)
		case "/page.png":
			w.Write([]byte("<html>sign in</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer pictures.Close()
	var uploads int
	var statement string
	exciter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/requirements/X-R1/attachments":
			w.Write([]byte(`{"attachments":[]}`))
		case "POST /api/v1/requirements/X-R1/attachments":
			if f, _, err := r.FormFile("file"); err != nil {
				t.Errorf("the upload has no file: %v", err)
			} else if got, _ := io.ReadAll(f); string(got) != string(png) {
				t.Errorf("uploaded %q", got)
			}
			uploads++
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"attachment":{"id":"att-1"}}`))
		case "GET /api/v1/requirements/X-R1":
			w.Write([]byte(`{"requirement":{"revision":3}}`))
		case "PATCH /api/v1/requirements/X-R1":
			var body struct {
				BaseRevision      int    `json:"baseRevision"`
				StatementMarkdown string `json:"statementMarkdown"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.BaseRevision != 3 {
				t.Errorf("changed revision %d, want 3", body.BaseRevision)
			}
			statement = body.StatementMarkdown
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer exciter.Close()
	var warnings []string
	im := &importer{exciter: newSession("exciter", exciter.URL), web: pictures.Client(),
		note: func(string, ...any) {}, warn: func(f string, a ...any) { warnings = append(warnings, fmt.Sprintf(f, a...)) }}
	issue := sampleIssue{Body: "![shot](" + pictures.URL + "/a.png) ![gone](" + pictures.URL + "/gone.png) ![login](" + pictures.URL + "/page.png)"}
	im.exciterPictures(context.Background(), "X-R1", issue)
	want := "![shot](exciter:attachment/att-1) [gone](" + pictures.URL + "/gone.png) [login](" + pictures.URL + "/page.png)"
	if uploads != 1 || statement != want {
		t.Fatalf("uploaded %d and wrote %q, want 1 and %q", uploads, statement, want)
	}
	if len(warnings) != 2 {
		t.Fatalf("warned %q, want one for each picture that stays a link", warnings)
	}
}

func TestAStatementKeepsItsTextWithoutHTML(t *testing.T) {
	in := "<!-- template -->\n<details><summary>Logs</summary>\nline one<br>line two\n</details>\nUse `List<T>` here.\n```html\n<div>kept</div>\n```"
	want := "\nLogs\nline one  \nline two\n\nUse `List<T>` here.\n```html\n<div>kept</div>\n```"
	if got := withoutHTML(in); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestAPageLinkingToAGonePageIsWrittenAgain(t *testing.T) {
	const gone, kept = "01a15fcf-264a-79e4-b4da-2cdc47674346", "01a15fcf-27e6-7f39-ad10-1a4cef27844c"
	bodies := map[string]string{"old": "[x](STATOR/p/" + gone + ")", "fine": "[x](STATOR/p/" + kept + ") [y](https://elsewhere/p/" + gone + ")"}
	var stator *httptest.Server
	stator = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, md := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/pages/"), "/markdown")
		switch {
		case md:
			w.Write([]byte(strings.ReplaceAll(bodies[id], "STATOR", stator.URL)))
		case id == "new":
			w.Write([]byte(`{"page":{"version":1}}`))
		default:
			w.Write([]byte(`{"page":{"version":4}}`))
		}
	}))
	defer stator.Close()
	im := &importer{stator: newSession("stator", stator.URL), statorURL: stator.URL}
	alive := map[string]bool{kept: true}
	for id, want := range map[string]bool{"old": true, "fine": false, "new": true} {
		if stale, err := im.statorStale(context.Background(), id, alive); err != nil || stale != want {
			t.Errorf("the page %s is stale %v (%v), want %v", id, stale, err, want)
		}
	}
}
