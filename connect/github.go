package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// github reads a public repository's issues and documentation. raw serves a
// file's content at a commit, which costs no API request.
type github struct {
	api, raw string
	token    string
	client   *http.Client
}

func newGitHub(token string) *github {
	return &github{api: "https://api.github.com", raw: "https://raw.githubusercontent.com", token: token,
		client: &http.Client{Timeout: 60 * time.Second}}
}

// maxDocs bounds how much documentation one source brings from its repository
// and from its docs site each, as maxIssues does its issues.
const maxDocs = 500

func (g *github) project(ctx context.Context, src sampleSource, maxIssues int) (sampleProject, error) {
	p := sampleProject{Repo: src.repo, Key: src.key}
	var repo struct {
		Name          string `json:"name"`
		DefaultBranch string `json:"default_branch"`
		License       *struct {
			SPDX string `json:"spdx_id"`
		} `json:"license"`
	}
	if err := g.get(ctx, "/repos/"+src.repo, &repo); err != nil {
		return p, err
	}
	p.Name = repo.Name
	if repo.License != nil {
		p.License = repo.License.SPDX
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := g.get(ctx, "/repos/"+src.repo+"/commits/"+url.PathEscape(repo.DefaultBranch), &commit); err != nil {
		return p, err
	}
	p.Commit = commit.SHA

	issues, err := g.issues(ctx, src.repo, maxIssues)
	if err != nil {
		return p, err
	}
	p.Issues = issues
	docs, err := g.docs(ctx, src.repo, commit.SHA, func(file string) bool {
		switch lower := strings.ToLower(file); {
		case lower == "readme.md", lower == "changelog.md":
			return true
		case strings.HasPrefix(lower, "docs/"):
			return isDoc(file)
		}
		return false
	})
	if err != nil {
		return p, err
	}
	p.Docs = docs
	if s := src.site; s != nil {
		site, err := g.siteDocs(ctx, *s)
		if err != nil {
			return p, err
		}
		p.Docs = append(p.Docs, site...)
		p.Sites = []docsSite{*s}
	}
	return p, nil
}

// siteDocs are the pages of a docs site, from its repository's default branch.
func (g *github) siteDocs(ctx context.Context, s docsSite) ([]sampleDoc, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.get(ctx, "/repos/"+s.Repo, &repo); err != nil {
		return nil, err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := g.get(ctx, "/repos/"+s.Repo+"/commits/"+url.PathEscape(repo.DefaultBranch), &commit); err != nil {
		return nil, err
	}
	prefix := s.Dir + "/"
	if s.Dir == "" {
		prefix = ""
	}
	docs, err := g.docs(ctx, s.Repo, commit.SHA, func(file string) bool {
		rel, ok := strings.CutPrefix(file, prefix)
		if !ok || !isDoc(file) {
			return false
		}
		for _, x := range s.Exclude {
			if rel == x || strings.HasPrefix(rel, strings.TrimSuffix(x, "/")+"/") {
				return false
			}
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	for i := range docs {
		docs[i].Repo, docs[i].File = s.Repo, docs[i].Path
		docs[i].Path = s.space() + "/" + strings.TrimPrefix(docs[i].Path, prefix)
	}
	return docs, nil
}

// issues are the newest first, pull requests left out, at most max; 0 means all.
func (g *github) issues(ctx context.Context, repo string, max int) ([]sampleIssue, error) {
	var out []sampleIssue
	for page := 1; ; page++ {
		var batch []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			Body   string `json:"body"`
			State  string `json:"state"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
			Milestone *struct {
				Title string `json:"title"`
			} `json:"milestone"`
			PullRequest json.RawMessage `json:"pull_request"`
		}
		path := fmt.Sprintf("/repos/%s/issues?state=all&sort=created&direction=desc&per_page=100&page=%d", repo, page)
		if err := g.get(ctx, path, &batch); err != nil {
			return nil, err
		}
		for _, i := range batch {
			if i.PullRequest != nil {
				continue
			}
			issue := sampleIssue{Number: i.Number, Title: i.Title, Body: i.Body, Closed: i.State == "closed"}
			for _, l := range i.Labels {
				issue.Labels = append(issue.Labels, l.Name)
			}
			if i.Milestone != nil {
				issue.Milestone = i.Milestone.Title
			}
			out = append(out, issue)
			if max > 0 && len(out) == max {
				return out, nil
			}
		}
		if len(batch) < 100 {
			return out, nil
		}
	}
}

// docs are the files keep takes, at commit.
func (g *github) docs(ctx context.Context, repo, commit string, keep func(string) bool) ([]sampleDoc, error) {
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := g.get(ctx, "/repos/"+repo+"/git/trees/"+commit+"?recursive=1", &tree); err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range tree.Tree {
		if e.Type == "blob" && keep(e.Path) {
			paths = append(paths, e.Path)
		}
	}
	// README first, as the space's home; the rest by path, so a rerun meets them in the same order.
	sort.SliceStable(paths, func(a, b int) bool {
		ra, rb := strings.EqualFold(paths[a], "readme.md"), strings.EqualFold(paths[b], "readme.md")
		if ra != rb {
			return ra
		}
		return paths[a] < paths[b]
	})
	if len(paths) > maxDocs {
		paths = paths[:maxDocs]
	}
	var out []sampleDoc
	for _, p := range paths {
		body, err := g.fetch(ctx, g.raw+"/"+repo+"/"+commit+"/"+p)
		if err != nil {
			return nil, err
		}
		out = append(out, sampleDoc{Path: p, Markdown: string(body)})
	}
	return out, nil
}

func (g *github) get(ctx context.Context, path string, out any) error {
	body, err := g.fetch(ctx, g.api+path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GitHub answered %s with something other than JSON: %w", path, err)
	}
	return nil
}

func (g *github) fetch(ctx context.Context, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach GitHub for %s: %w", address, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0",
		resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("GitHub's rate limit is used up (%s); give dynamo-connect a GitHub token (GITHUB_TOKEN, or the chart's sampleData.github.tokenSecret), which allows 5000 requests an hour, or take fewer issues", address)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GitHub answered %s with %d: %s", address, resp.StatusCode, strings.TrimSpace(string(body[:min(len(body), 200)])))
	}
	return body, nil
}
