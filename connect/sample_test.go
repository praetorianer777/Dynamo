package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestTheSourcesAreNamedOrGivenAsRepositories(t *testing.T) {
	got, err := parseSources(" evcc, stator=STA ,owner/my-tool-2 ,")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, s := range got {
		keys = append(keys, s.repo+"="+s.key)
	}
	if want := "evcc-io/evcc=EVCC praetorianer777/Stator=STA owner/my-tool-2=MYTOOL2"; strings.Join(keys, " ") != want {
		t.Fatalf("got %v, want %s", keys, want)
	}
	if got[0].site == nil || got[0].site.Repo != "evcc-io/docs" || got[2].site != nil {
		t.Errorf("the docs sites are %+v and %+v", got[0].site, got[2].site)
	}
	for _, bad := range []string{"nosuchname", "evcc=lower", "a/b/c", "owner/x=TOOLONGKEYHERE", `[{"repo":"o/r","docs":{"url":"docs.example"}}]`, `["evcc"`} {
		if _, err := parseSources(bad); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
}

func TestTheBuiltInSnapshotDescribesAProject(t *testing.T) {
	p, err := builtInSnapshot("sparkplug")
	if err != nil {
		t.Fatal(err)
	}
	if p.Key != "SPARK" || len(p.Issues) != 4 || len(p.Docs) != 6 || len(p.Sites) != 1 {
		t.Fatalf("read %s %s with %d issues and %d docs", p.Repo, p.Key, len(p.Issues), len(p.Docs))
	}
	var features []int
	for _, i := range p.Issues {
		if i.feature() {
			features = append(features, i.Number)
		}
	}
	if !slices.Equal(features, []int{1, 3}) {
		t.Errorf("the features are %v, want [1 3]", features)
	}
	if got := p.sourceKey(p.Issues[1]); got != "dynamo-sample/sparkplug#2" {
		t.Errorf("the source key is %q", got)
	}
	readme := p.Docs[0]
	if readme.title() != "Sparkplug" || !slices.Equal(readme.mentions(), []int{1, 3}) {
		t.Errorf("the README is %q, mentioning %v", readme.title(), readme.mentions())
	}
	if (sampleDoc{Path: "docs/no-heading.md", Markdown: "text only"}).title() != "No heading" {
		t.Error("a doc without a heading is not named by its file")
	}

	var buf bytes.Buffer
	if err := writeSnapshot(&buf, p); err != nil {
		t.Fatal(err)
	}
	again, err := readSnapshot(&buf)
	if err != nil || again.Repo != p.Repo || len(again.Issues) != len(p.Issues) {
		t.Fatalf("a written snapshot reads back as %+v: %v", again, err)
	}
	if _, err := readSnapshot(strings.NewReader(`{"repo":"a/b","key":"bad key"}`)); err == nil {
		t.Error("a snapshot with a bad key was taken")
	}
}

func TestMentionsAreIssueNumbersOnly(t *testing.T) {
	d := sampleDoc{Markdown: "Fixed in #12 and #12 again, see #7. Not &#38; or a/#9 or #x or x#5."}
	if got := d.mentions(); !slices.Equal(got, []int{12, 7}) {
		t.Fatalf("mentions %v", got)
	}
}

func TestAnIssueMentionsOthersButNotItself(t *testing.T) {
	i := sampleIssue{Number: 5, Body: "Like #3, unlike #5, see #3 and #8."}
	if got := i.mentions(); !slices.Equal(got, []int{3, 8}) {
		t.Fatalf("mentions %v", got)
	}
}

func TestASourceNamesItsDocsSiteInJSON(t *testing.T) {
	got, err := parseSources(`["evcc=EV", {"repo": "owner/tool", "key": "TOOL", "docs": {"url": "https://docs.tool.example/", "repo": "owner/tool-docs", "dir": "/content/en/"}}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].key != "EV" || got[1].key != "TOOL" {
		t.Fatalf("got %+v", got)
	}
	if s := got[1].site; s == nil || s.URL != "https://docs.tool.example" || s.Dir != "content/en" || s.space() != "docs.tool.example" {
		t.Fatalf("the docs site is %+v", got[1].site)
	}
}
