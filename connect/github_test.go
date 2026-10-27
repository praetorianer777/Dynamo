package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGitHub serves one repository with 150 issues and pull requests mixed in,
// and records whether requests carried the token.
func fakeGitHub(t *testing.T) (*github, *[]string) {
	t.Helper()
	var auth []string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/tool", func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		fmt.Fprint(w, `{"name":"tool","default_branch":"main","license":{"spdx_id":"MIT"}}`)
	})
	mux.HandleFunc("/repos/o/tool/commits/main", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"sha":"abc123"}`)
	})
	mux.HandleFunc("/repos/o/tool/issues", func(w http.ResponseWriter, r *http.Request) {
		var page int
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		var batch []map[string]any
		for n := 200 - (page-1)*100; n > 200-page*100 && n > 0; n-- {
			item := map[string]any{"number": n, "title": fmt.Sprintf("Issue %d", n), "body": "b", "state": "open",
				"labels": []map[string]string{{"name": "enhancement"}}}
			if n%4 == 0 {
				item["pull_request"] = map[string]string{"url": "x"}
			}
			if n%5 == 0 {
				item["state"] = "closed"
				item["milestone"] = map[string]string{"title": "2.0"}
			}
			batch = append(batch, item)
		}
		json.NewEncoder(w).Encode(batch)
	})
	mux.HandleFunc("/repos/o/tool/git/trees/abc123", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tree":[
			{"path":"docs/b.md","type":"blob"},{"path":"src/notes.md","type":"blob"},{"path":"docs","type":"tree"},
			{"path":"README.md","type":"blob"},{"path":"docs/a.md","type":"blob"},{"path":"CHANGELOG.md","type":"blob"}]}`)
	})
	mux.HandleFunc("/repos/o/tool-docs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"name":"tool-docs","default_branch":"live"}`)
	})
	mux.HandleFunc("/repos/o/tool-docs/commits/live", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"sha":"def456"}`)
	})
	mux.HandleFunc("/repos/o/tool-docs/git/trees/def456", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tree":[
			{"path":"README.md","type":"blob"},{"path":"content/en/index.mdx","type":"blob"},{"path":"content/en/old/x.md","type":"blob"},
			{"path":"content/en/guide/keras.md","type":"blob"},{"path":"content/en/logo.png","type":"blob"}]}`)
	})
	mux.HandleFunc("/raw/o/tool-docs/def456/content/en/index.mdx", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "---\ntitle: Docs\n---\nWelcome.")
	})
	mux.HandleFunc("/raw/o/tool-docs/def456/content/en/guide/keras.md", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "# Keras")
	})
	mux.HandleFunc("/raw/o/tool/abc123/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "# %s\n", strings.TrimPrefix(r.URL.Path, "/raw/o/tool/abc123/"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &github{api: srv.URL, raw: srv.URL + "/raw", token: "t0ken", client: srv.Client()}, &auth
}

func TestAProjectIsReadFromGitHub(t *testing.T) {
	g, auth := fakeGitHub(t)
	p, err := g.project(context.Background(), sampleSource{repo: "o/tool", key: "TOOL"}, 120)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "tool" || p.License != "MIT" || p.Commit != "abc123" || (*auth)[0] != "Bearer t0ken" {
		t.Fatalf("read %+v, auth %v", p, *auth)
	}
	if len(p.Issues) != 120 || p.Issues[0].Number != 199 {
		t.Fatalf("read %d issues starting at %d, want 120 of the newest without pull requests", len(p.Issues), p.Issues[0].Number)
	}
	for _, i := range p.Issues {
		if i.Number%4 == 0 {
			t.Fatalf("pull request %d was read as an issue", i.Number)
		}
		if i.Number%5 == 0 && (!i.Closed || i.Milestone != "2.0") {
			t.Fatalf("issue %d lost its state or milestone: %+v", i.Number, i)
		}
	}
	var paths []string
	for _, d := range p.Docs {
		paths = append(paths, d.Path)
		if d.title() != d.Path {
			t.Errorf("%s read as %q", d.Path, d.Markdown)
		}
	}
	if got := strings.Join(paths, " "); got != "README.md CHANGELOG.md docs/a.md docs/b.md" {
		t.Errorf("the docs are %s", got)
	}

	all, err := g.issues(context.Background(), "o/tool", 0)
	if err != nil || len(all) != 150 {
		t.Fatalf("all issues: %d, %v", len(all), err)
	}
}

func TestARateLimitSaysToSetAToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	g := &github{api: srv.URL, client: srv.Client()}
	_, err := g.project(context.Background(), sampleSource{repo: "o/tool", key: "TOOL"}, 10)
	if err == nil {
		t.Fatal("a used-up rate limit gave no error")
	}
	for _, setting := range []string{"GITHUB_TOKEN", "sampleData.github.tokenSecret"} {
		if !strings.Contains(err.Error(), setting) {
			t.Errorf("the error does not name %s: %v", setting, err)
		}
	}
}

func TestADocsSiteIsReadWithItsPages(t *testing.T) {
	g, _ := fakeGitHub(t)
	site := docsSite{URL: "https://docs.tool.example", Repo: "o/tool-docs", Dir: "content/en", Exclude: []string{"old"}}
	p, err := g.project(context.Background(), sampleSource{repo: "o/tool", key: "TOOL", site: &site}, 1)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range p.Docs {
		if d.Repo == "o/tool-docs" {
			got = append(got, d.Path+"="+d.File+":"+d.title())
		}
	}
	if want := "docs.tool.example/guide/keras.md=content/en/guide/keras.md:Keras docs.tool.example/index.mdx=content/en/index.mdx:Docs"; strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
	if len(p.Sites) != 1 || p.Sites[0].Repo != "o/tool-docs" {
		t.Fatalf("the sites are %+v", p.Sites)
	}
}
