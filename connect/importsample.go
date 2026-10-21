package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// importer fills the three tools with one sample project each. Every step
// looks first and only makes what is missing, so a rerun changes nothing.
type importer struct {
	armature, stator, exciter *session
	// The addresses people open, which the links between the tools name.
	armatureURL, statorURL, exciterURL string
	note                               func(string, ...any)
	warn                               func(string, ...any)
	// web fetches the pictures of issues; nil is a client of its own.
	web *http.Client
}

// numbered is the marker a sample item carries in its title, as GitHub writes
// a reference; a rerun finds what an earlier one made by it.
var numbered = regexp.MustCompile(`\(#(\d+)\)$`)

func numberOf(title string) (int, bool) {
	m := numbered.FindStringSubmatch(title)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

func markedTitle(title string, number int) string {
	const max = 200
	if r := []rune(title); len(r) > max {
		title = string(r[:max]) + "..."
	}
	return fmt.Sprintf("%s (#%d)", strings.TrimSpace(title), number)
}

func (im *importer) project(ctx context.Context, p sampleProject) error {
	issues, err := im.armatureIssues(ctx, p)
	if err != nil {
		return err
	}
	pages, err := im.statorPages(ctx, p, issues)
	if err != nil {
		return err
	}
	for _, d := range p.Docs {
		page, ok := pages[d.Path]
		if !ok || !p.own(d) {
			continue
		}
		for _, n := range d.mentions() {
			issue, ok := issues[n]
			if !ok {
				continue
			}
			if err := im.statorLink(ctx, page, issue); err != nil {
				return err
			}
			if err := im.armatureLink(ctx, issue, page); err != nil {
				return err
			}
		}
	}
	if about, ok := pages[aboutPath]; ok {
		for _, l := range []struct{ url, title, source string }{
			{im.armatureProject(p), "Armature project " + p.Key, "Armature"},
			{im.exciterProject(p), "Exciter project " + p.Key, "Exciter"},
		} {
			exists, err := hasRemoteLink(ctx, im.stator, "/pages/"+about.id+"/remote-links", l.url)
			if err != nil {
				return err
			}
			if exists {
				continue
			}
			body := map[string]string{"url": l.url, "title": l.title, "source": l.source}
			if err := im.stator.send(ctx, http.MethodPost, "/pages/"+about.id+"/remote-links", body, nil); err != nil {
				return err
			}
			im.note("the Stator page %q links to %s", about.title, l.title)
		}
	}
	if err := im.armatureRelate(ctx, p, issues); err != nil {
		return err
	}
	return im.exciterRequirements(ctx, p, issues, pages)
}

// armatureRelate relates each issue to the issues its body mentions, with the
// link type whose name says relates, the one every Armature project has.
func (im *importer) armatureRelate(ctx context.Context, p sampleProject, issues map[int]armatureIssue) error {
	var types struct {
		LinkTypes []struct {
			Name string `json:"name"`
		} `json:"linkTypes"`
	}
	if err := im.armature.send(ctx, http.MethodGet, "/link-types", nil, &types); err != nil {
		return err
	}
	relates := ""
	for _, t := range types.LinkTypes {
		if strings.Contains(strings.ToLower(t.Name), "relat") {
			relates = t.Name
			break
		}
	}
	for _, i := range p.Issues {
		from, ok := issues[i.Number]
		if !ok {
			continue
		}
		var have map[string]bool
		for _, n := range i.mentions() {
			to, ok := issues[n]
			if !ok {
				continue
			}
			if relates == "" {
				im.warn("%s is not related to %s: Armature has no link type that relates issues", from.key, to.key)
				continue
			}
			if have == nil {
				var list struct {
					Links []struct {
						Issue struct {
							Key string `json:"key"`
						} `json:"issue"`
					} `json:"links"`
				}
				if err := im.armature.send(ctx, http.MethodGet, "/issues/"+from.key+"/links", nil, &list); err != nil {
					return err
				}
				have = map[string]bool{}
				for _, l := range list.Links {
					have[l.Issue.Key] = true
				}
			}
			if have[to.key] {
				continue
			}
			body := map[string]string{"type": relates, "targetKey": to.key}
			if err := im.armature.send(ctx, http.MethodPost, "/issues/"+from.key+"/links", body, nil); err != nil {
				return err
			}
			have[to.key] = true
			im.note("%s relates to %s", from.key, to.key)
		}
	}
	return nil
}

type armatureIssue struct{ key, summary string }

func (im *importer) armatureIssues(ctx context.Context, p sampleProject) (map[int]armatureIssue, error) {
	err := im.armature.send(ctx, http.MethodGet, "/projects/"+p.Key, nil, nil)
	switch {
	case hasStatus(err, http.StatusNotFound):
		body := map[string]string{"key": p.Key, "name": p.Name}
		if err := im.armature.send(ctx, http.MethodPost, "/projects", body, nil); err != nil {
			return nil, err
		}
		im.note("Armature has the sample project %s from %s", p.Key, p.Repo)
	case err != nil:
		return nil, err
	}

	types, err := im.armatureTypes(ctx)
	if err != nil {
		return nil, err
	}
	have := map[int]armatureIssue{}
	typed := map[int]string{}
	for offset := 0; ; {
		var page struct {
			Issues []struct {
				Key     string `json:"key"`
				Summary string `json:"summary"`
				Type    struct {
					Name string `json:"name"`
				} `json:"type"`
			} `json:"issues"`
			Total int `json:"total"`
		}
		path := fmt.Sprintf("/projects/%s/issues?limit=100&offset=%d", p.Key, offset)
		if err := im.armature.send(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		for _, i := range page.Issues {
			if n, ok := numberOf(i.Summary); ok {
				have[n] = armatureIssue{key: i.Key, summary: i.Summary}
				typed[n] = i.Type.Name
			}
		}
		offset += len(page.Issues)
		if len(page.Issues) == 0 || offset >= page.Total {
			break
		}
	}

	labels, err := im.armatureLabels(ctx)
	if err != nil {
		return nil, err
	}
	for _, i := range p.Issues {
		want := types[i.kind()]
		if issue, ok := have[i.Number]; ok {
			// Earlier imports made every issue a Task; one somebody retyped stays as it is.
			if typed[i.Number] == "Task" && i.kind() != "Task" && want != "" {
				if err := im.armature.send(ctx, http.MethodPatch, "/issues/"+issue.key, map[string]string{"typeId": want}, nil); err != nil {
					return nil, err
				}
				im.note("%s is a %s", issue.key, i.kind())
			}
			continue
		}
		body := map[string]any{"summary": markedTitle(i.Title, i.Number)}
		if want != "" {
			body["typeId"] = want
		}
		if doc := richText(i.Body); doc != nil {
			body["description"] = doc
		}
		var made struct {
			Issue struct {
				Key     string `json:"key"`
				Summary string `json:"summary"`
			} `json:"issue"`
		}
		if err := im.armature.send(ctx, http.MethodPost, "/projects/"+p.Key+"/issues", body, &made); err != nil {
			return nil, err
		}
		issue := armatureIssue{key: made.Issue.Key, summary: made.Issue.Summary}
		have[i.Number] = issue
		im.note("Armature has %s from %s", issue.key, p.sourceKey(i))

		if err := im.armatureLabel(ctx, issue.key, i.Labels, labels); err != nil {
			return nil, err
		}
		origin := map[string]string{"url": fmt.Sprintf("https://github.com/%s/issues/%d", p.Repo, i.Number), "title": p.sourceKey(i), "source": "GitHub"}
		if err := im.armature.send(ctx, http.MethodPost, "/issues/"+issue.key+"/remote-links", origin, nil); err != nil {
			return nil, err
		}
		if i.Closed {
			if err := im.armatureClose(ctx, issue.key); err != nil {
				return nil, err
			}
		}
	}
	return have, nil
}

// armatureTypes are the ids of Armature's issue types by name.
func (im *importer) armatureTypes(ctx context.Context) (map[string]string, error) {
	var list struct {
		IssueTypes []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"issueTypes"`
	}
	if err := im.armature.send(ctx, http.MethodGet, "/issue-types", nil, &list); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, t := range list.IssueTypes {
		out[t.Name] = t.ID
	}
	return out, nil
}

func (im *importer) armatureLabels(ctx context.Context) (map[string]bool, error) {
	var list struct {
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := im.armature.send(ctx, http.MethodGet, "/labels", nil, &list); err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, l := range list.Labels {
		have[strings.ToLower(l.Name)] = true
	}
	return have, nil
}

// armatureLabel gives an issue its labels in Armature's form, making those
// Armature lacks; a label it refuses all the same is left out with a warning.
func (im *importer) armatureLabel(ctx context.Context, key string, names []string, have map[string]bool) error {
	var labels []string
	for _, n := range labelNames(names) {
		if !have[strings.ToLower(n)] {
			err := im.armature.send(ctx, http.MethodPost, "/labels", map[string]string{"name": n, "color": "gray"}, nil)
			switch {
			case hasStatus(err, http.StatusBadRequest):
				im.warn("%s is without the label %q: %v", key, n, err)
				continue
			case err != nil && !hasStatus(err, http.StatusConflict):
				return err
			}
			have[strings.ToLower(n)] = true
		}
		labels = append(labels, n)
	}
	if len(labels) == 0 {
		return nil
	}
	return im.armature.send(ctx, http.MethodPut, "/issues/"+key+"/labels", map[string][]string{"labels": labels}, nil)
}

// maxLabel is the longest label Armature takes, in characters.
const maxLabel = 40

// labelNames are GitHub labels as Armature takes them, one word of at most
// maxLabel characters each, spaces as hyphens, once each.
func labelNames(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.Join(strings.Fields(n), "-")
		if r := []rune(n); len(r) > maxLabel {
			n = string(r[:maxLabel])
		}
		if n == "" || seen[strings.ToLower(n)] {
			continue
		}
		seen[strings.ToLower(n)] = true
		out = append(out, n)
	}
	return out
}

var doneTransition = regexp.MustCompile(`(?i)\b(done|close|closed|resolve|resolved|complete|finish)\b`)

// armatureClose moves an issue along the transition named like done, which a
// project's default workflow has; another workflow leaves it open, with a warning.
func (im *importer) armatureClose(ctx context.Context, key string) error {
	var list struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"transitions"`
	}
	if err := im.armature.send(ctx, http.MethodGet, "/issues/"+key+"/transitions", nil, &list); err != nil {
		return err
	}
	for _, t := range list.Transitions {
		if doneTransition.MatchString(t.Name) {
			return im.armature.send(ctx, http.MethodPost, "/issues/"+key+"/transitions", map[string]string{"transitionId": t.ID}, nil)
		}
	}
	im.warn("%s is closed on GitHub but stays open: its workflow has no transition named like done", key)
	return nil
}

func (im *importer) armatureLink(ctx context.Context, issue armatureIssue, page statorPage) error {
	target := im.statorURL + "/p/" + page.id
	exists, err := hasRemoteLink(ctx, im.armature, "/issues/"+issue.key+"/remote-links", target)
	if err != nil || exists {
		return err
	}
	body := map[string]string{"url": target, "title": page.title, "source": "Stator", "relation": "page"}
	if err := im.armature.send(ctx, http.MethodPost, "/issues/"+issue.key+"/remote-links", body, nil); err != nil {
		return err
	}
	im.note("%s links to the Stator page %q", issue.key, page.title)
	return nil
}

type statorPage struct{ id, title string }

// aboutPath is the made-up path of the page that says where the sample came from.
const aboutPath = "about-this-sample.md"

func (im *importer) armatureProject(p sampleProject) string {
	return im.armatureURL + "/projects/" + p.Key
}
func (im *importer) exciterProject(p sampleProject) string { return im.exciterURL + "/p/" + p.Key }

// statorPages makes the project's space, with the README as its home page,
// a page for each other doc, and one saying where the sample came from.
func (im *importer) statorPages(ctx context.Context, p sampleProject, issues map[int]armatureIssue) (map[string]statorPage, error) {
	var space struct {
		Space struct {
			HomePageID string `json:"homePageId"`
		} `json:"space"`
	}
	fresh := false
	err := im.stator.send(ctx, http.MethodGet, "/spaces/"+p.Key, nil, &space)
	switch {
	case hasStatus(err, http.StatusNotFound):
		body := map[string]string{"key": p.Key, "name": p.Name}
		if err := im.stator.send(ctx, http.MethodPost, "/spaces", body, &space); err != nil {
			return nil, err
		}
		fresh = true
		im.note("Stator has the sample space %s from %s", p.Key, p.Repo)
	case err != nil:
		return nil, err
	}
	home := space.Space.HomePageID

	have, alive, err := im.statorSources(ctx, p.Key)
	if err != nil {
		return nil, err
	}
	about := sampleDoc{Path: aboutPath, Markdown: fmt.Sprintf(
		"# About this sample\n\nThis space, the [Armature project %s](%s) and the [Exciter project %s](%s) were imported by Dynamo from [%s](https://github.com/%s) at commit `%s`.\n\nLicence: %s. The content belongs to its authors.\n",
		p.Key, im.armatureProject(p), p.Key, im.exciterProject(p), p.Repo, p.Repo, p.Commit, p.License)}
	nodes := append(pageTree(p), pageNode{key: aboutPath, title: "About this sample", source: "https://github.com/" + p.Repo, doc: &about})

	// Every page is made before any is written, so each link can name the page it leads to.
	ids := map[string]string{}
	var write []pageNode
	for _, n := range nodes {
		if id, ok := have[n.source]; ok {
			ids[n.key] = id
			stale, err := im.statorStale(ctx, id, alive)
			if err != nil {
				return nil, err
			}
			if stale {
				write = append(write, n)
			}
			continue
		}
		var id string
		if n.key == "" {
			id = home
			if !fresh {
				// A space of the same key that this import did not make keeps its home.
				ids[n.key] = id
				continue
			}
		} else {
			var made struct {
				Page struct {
					ID string `json:"id"`
				} `json:"page"`
			}
			body := map[string]any{"parentId": ids[n.parent], "title": n.title, "publish": true}
			if err := im.stator.send(ctx, http.MethodPost, "/pages", body, &made); err != nil {
				return nil, err
			}
			id = made.Page.ID
			im.note("Stator has the page %q from %s", n.title, p.Repo)
		}
		repo, file, _ := strings.Cut(strings.TrimPrefix(n.source, "https://github.com/"), "/blob/HEAD/")
		if file == "" {
			repo, file, _ = strings.Cut(repo, "/tree/HEAD/")
		}
		link := map[string]string{"url": n.source, "title": strings.TrimSuffix(repo+"/"+file, "/"), "source": "GitHub"}
		if err := im.stator.send(ctx, http.MethodPost, "/pages/"+id+"/remote-links", link, nil); err != nil {
			return nil, err
		}
		ids[n.key] = id
		write = append(write, n)
	}

	pageURL := func(key string) (string, bool) {
		id, ok := ids[key]
		return im.statorURL + "/p/" + id, ok
	}
	issueURL := map[int]string{}
	for n, i := range issues {
		issueURL[n] = im.armatureURL + "/issues/" + i.key
	}
	links := newLinker(p, pageURL, issueURL)
	for _, n := range write {
		var md string
		if n.doc != nil {
			md = links.rewrite(*n.doc, cleanMarkdown(n.doc.Markdown, n.title))
		} else {
			md = folderMarkdown(n, nodes, pageURL)
		}
		name := withoutExt(path.Base(n.key))
		if n.key == "" {
			name = "index"
		}
		err := im.statorMarkdown(ctx, ids[n.key], name+".md", md)
		if hasStatus(err, http.StatusUnprocessableEntity) {
			// Stator refuses some Markdown that GitHub shows; the page then
			// says where to read it rather than stopping the whole import.
			im.warn("the Stator page %q holds only a link to its source, since Stator refused its Markdown: %v", n.title, err)
			md = fmt.Sprintf("# %s\n\nStator could not take this page's Markdown; read it [on GitHub](%s).\n", n.title, n.source)
			err = im.statorMarkdown(ctx, ids[n.key], name+".md", md)
		}
		if err != nil {
			return nil, err
		}
	}

	out := map[string]statorPage{}
	for _, n := range nodes {
		if n.doc != nil {
			out[n.doc.Path] = statorPage{id: ids[n.key], title: n.title}
		}
	}
	return out, nil
}

var pageID = regexp.MustCompile(`/p/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)

// statorStale says whether a page is to be written again: a run stopped
// between making and writing it left it at its first version, or it links to
// a page of the space that is gone, as one removed by hand.
func (im *importer) statorStale(ctx context.Context, id string, alive map[string]bool) (bool, error) {
	var current struct {
		Page struct {
			Version int `json:"version"`
		} `json:"page"`
	}
	if err := im.stator.send(ctx, http.MethodGet, "/pages/"+id, nil, &current); err != nil {
		return false, err
	}
	if current.Page.Version <= 1 {
		return true, nil
	}
	var md []byte
	if err := im.stator.send(ctx, http.MethodGet, "/pages/"+id+"/markdown", nil, &md); err != nil {
		return false, err
	}
	for _, m := range pageID.FindAllSubmatchIndex(md, -1) {
		if bytes.HasSuffix(md[:m[0]], []byte(im.statorURL)) && !alive[string(md[m[2]:m[3]])] {
			return true, nil
		}
	}
	return false, nil
}

// folderMarkdown is the page of a folder without a doc of its own: a list of the pages under it.
func folderMarkdown(folder pageNode, nodes []pageNode, pageURL func(string) (string, bool)) string {
	md := "# " + folder.title + "\n\n"
	for _, n := range nodes {
		if n.parent == folder.key && n.key != "" {
			address, _ := pageURL(n.key)
			md += fmt.Sprintf("- [%s](%s)\n", n.title, address)
		}
	}
	return md
}

// statorSources are the space's pages by the GitHub address of what each was
// imported from, which every imported page links to, and the ids of all its pages.
func (im *importer) statorSources(ctx context.Context, space string) (map[string]string, map[string]bool, error) {
	// Every page of the outline, since pages of one title, as two folders'
	// "Configuration", are pages of their own.
	var outline struct {
		Pages []struct {
			ID string `json:"id"`
		} `json:"pages"`
	}
	if err := im.stator.send(ctx, http.MethodGet, "/spaces/"+space+"/outline", nil, &outline); err != nil {
		return nil, nil, err
	}
	out, alive := map[string]string{}, map[string]bool{}
	for _, pg := range outline.Pages {
		id := pg.ID
		alive[id] = true
		var list struct {
			RemoteLinks []struct {
				URL    string `json:"url"`
				Source string `json:"source"`
			} `json:"remoteLinks"`
		}
		if err := im.stator.send(ctx, http.MethodGet, "/pages/"+id+"/remote-links", nil, &list); err != nil {
			return nil, nil, err
		}
		for _, l := range list.RemoteLinks {
			if l.Source == "GitHub" {
				out[l.URL] = id
			}
		}
	}
	return out, alive, nil
}

// statorMarkdown publishes a doc as the next version of the page, through
// Stator's own Markdown import, which also takes the doc's heading as its title.
func (im *importer) statorMarkdown(ctx context.Context, id, name, md string) error {
	var current struct {
		Page struct {
			Version int `json:"version"`
		} `json:"page"`
	}
	if err := im.stator.send(ctx, http.MethodGet, "/pages/"+id, nil, &current); err != nil {
		return err
	}
	path := fmt.Sprintf("/pages/%s/markdown?version=%d", id, current.Page.Version)
	return im.stator.upload(ctx, http.MethodPut, path, "file", name, []byte(md), nil)
}

func (im *importer) statorLink(ctx context.Context, page statorPage, issue armatureIssue) error {
	target := im.armatureURL + "/issues/" + issue.key
	exists, err := hasRemoteLink(ctx, im.stator, "/pages/"+page.id+"/remote-links", target)
	if err != nil || exists {
		return err
	}
	body := map[string]string{"url": target, "title": issue.key + " " + issue.summary, "source": "Armature"}
	if err := im.stator.send(ctx, http.MethodPost, "/pages/"+page.id+"/remote-links", body, nil); err != nil {
		return err
	}
	im.note("the Stator page %q links to %s", page.title, issue.key)
	return nil
}

func hasRemoteLink(ctx context.Context, s *session, path, target string) (bool, error) {
	var list struct {
		RemoteLinks []struct {
			URL string `json:"url"`
		} `json:"remoteLinks"`
	}
	if err := s.send(ctx, http.MethodGet, path, nil, &list); err != nil {
		return false, err
	}
	for _, l := range list.RemoteLinks {
		if l.URL == target {
			return true, nil
		}
	}
	return false, nil
}

// exciterRequirements makes one requirement per feature issue, traced to the
// Armature issue and to the Stator pages that mention it.
func (im *importer) exciterRequirements(ctx context.Context, p sampleProject, issues map[int]armatureIssue, pages map[string]statorPage) error {
	err := im.exciter.send(ctx, http.MethodGet, "/projects/"+p.Key, nil, nil)
	switch {
	case hasStatus(err, http.StatusNotFound):
		if err := im.exciter.send(ctx, http.MethodPost, "/projects", map[string]string{"key": p.Key, "name": p.Name}, nil); err != nil {
			return err
		}
		im.note("Exciter has the sample project %s from %s", p.Key, p.Repo)
	case err != nil:
		return err
	}

	have := map[int]string{}
	for offset := 0; ; {
		var page struct {
			Rows []struct {
				Key   string `json:"key"`
				Title string `json:"title"`
			} `json:"rows"`
			Total int `json:"total"`
		}
		path := fmt.Sprintf("/projects/%s/requirements?limit=100&offset=%d", p.Key, offset)
		if err := im.exciter.send(ctx, http.MethodGet, path, nil, &page); err != nil {
			return err
		}
		for _, r := range page.Rows {
			if n, ok := numberOf(r.Title); ok {
				have[n] = r.Key
			}
		}
		offset += len(page.Rows)
		if len(page.Rows) == 0 || offset >= page.Total {
			break
		}
	}

	for _, i := range p.Issues {
		if !i.feature() {
			continue
		}
		key, ok := have[i.Number]
		if !ok {
			var made struct {
				Requirement struct {
					Key string `json:"key"`
				} `json:"requirement"`
			}
			body := map[string]string{"title": markedTitle(i.Title, i.Number), "statementMarkdown": statementOf(i, nil)}
			err := im.exciter.send(ctx, http.MethodPost, "/projects/"+p.Key+"/requirements", body, &made)
			if hasStatus(err, http.StatusUnprocessableEntity) {
				im.warn("%s has no requirement in Exciter, which refused it: %v", p.sourceKey(i), err)
				continue
			}
			if err != nil {
				return err
			}
			key = made.Requirement.Key
			have[i.Number] = key
			im.note("Exciter has %s from %s", key, p.sourceKey(i))
		}
		im.exciterPictures(ctx, key, i)
		if issue, ok := issues[i.Number]; ok {
			im.exciterTrace(ctx, key, "/armature-links", "issueKey", issue.key, "implements", issue.key)
		}
		for _, d := range p.Docs {
			if page, ok := pages[d.Path]; ok && p.own(d) && mentions(d, i.Number) {
				im.exciterTrace(ctx, key, "/stator-links", "pageId", page.id, "describes", fmt.Sprintf("the Stator page %q", page.title))
			}
		}
	}
	for _, i := range p.Issues {
		if key, ok := have[i.Number]; ok && i.feature() {
			im.exciterRelate(ctx, key, i.mentions(), have)
		}
	}
	return nil
}

// exciterRelate links a requirement to the requirements of the features its
// issue mentions, with the link type relates.
func (im *importer) exciterRelate(ctx context.Context, key string, mentions []int, reqs map[int]string) {
	var linked map[string]bool
	for _, n := range mentions {
		other, ok := reqs[n]
		if !ok {
			continue
		}
		if linked == nil {
			var list struct {
				Links []struct {
					Other struct {
						Key string `json:"key"`
					} `json:"other"`
				} `json:"links"`
			}
			if err := im.exciter.send(ctx, http.MethodGet, "/requirements/"+url.PathEscape(key)+"/links", nil, &list); err != nil {
				im.warn("%s is not related to %s: %v", key, other, err)
				return
			}
			linked = map[string]bool{}
			for _, l := range list.Links {
				linked[l.Other.Key] = true
			}
		}
		if linked[other] {
			continue
		}
		body := map[string]string{"typeKey": "relates", "otherKey": other}
		if err := im.exciter.send(ctx, http.MethodPost, "/requirements/"+url.PathEscape(key)+"/links", body, nil); err != nil {
			im.warn("%s is not related to %s: %v", key, other, err)
			continue
		}
		linked[other] = true
		im.note("%s relates to %s", key, other)
	}
}

// exciterTrace links a requirement to the other tools. A refusal is a
// warning, since the requirement itself is there and the import goes on.
func (im *importer) exciterTrace(ctx context.Context, req, kind, field, target, relation, what string) {
	var list struct {
		Links []map[string]any `json:"links"`
	}
	if err := im.exciter.send(ctx, http.MethodGet, "/requirements/"+url.PathEscape(req)+kind, nil, &list); err == nil {
		for _, l := range list.Links {
			for _, v := range l {
				if fmt.Sprint(v) == target {
					return
				}
			}
		}
	}
	body := map[string]string{field: target, "relation": relation}
	if err := im.exciter.send(ctx, http.MethodPost, "/requirements/"+url.PathEscape(req)+kind, body, nil); err != nil {
		im.warn("%s is not traced to %s: %v", req, what, err)
		return
	}
	im.note("%s is traced to %s", req, what)
}

func mentions(d sampleDoc, number int) bool {
	for _, n := range d.mentions() {
		if n == number {
			return true
		}
	}
	return false
}

// maxText bounds one issue's text: some projects' issues carry whole logs.
const maxText = 20000

var (
	markdownImage = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)[^)]*\)`)
	htmlImage     = regexp.MustCompile(`<img\b[^>]*\bsrc="([^"]+)"[^>]*>`)
	htmlAlt       = regexp.MustCompile(`\balt="([^"]*)"`)
)

// statementOf is an issue's body as a requirement's statement, with the
// pictures attached holds by their address shown and the others as links.
func statementOf(i sampleIssue, attached map[string]string) string {
	statement := strings.TrimSpace(i.Body)
	if statement == "" {
		statement = i.Title
	}
	return truncate(withoutHTML(withPictures(statement, attached)), maxText)
}

// pictureURLs are the addresses of the pictures a text shows, once each, in order.
func pictureURLs(md string) []string {
	var out []string
	seen := map[string]bool{}
	for _, re := range []*regexp.Regexp{markdownImage, htmlImage} {
		for _, m := range re.FindAllStringSubmatch(md, -1) {
			u := m[len(m)-1]
			if strings.HasPrefix(u, "https://") && !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	return out
}

// withPictures shows each picture attached holds as that attachment and makes
// the rest links, since Exciter shows only pictures attached to the requirement.
func withPictures(md string, attached map[string]string) string {
	spell := func(alt, src string) string {
		if id, ok := attached[src]; ok {
			return "![" + alt + "](exciter:attachment/" + id + ")"
		}
		if alt == "" {
			alt = "image"
		}
		return "[" + alt + "](" + src + ")"
	}
	md = markdownImage.ReplaceAllStringFunc(md, func(m string) string {
		sub := markdownImage.FindStringSubmatch(m)
		return spell(sub[1], sub[2])
	})
	return htmlImage.ReplaceAllStringFunc(md, func(m string) string {
		alt := ""
		if a := htmlAlt.FindStringSubmatch(m); a != nil {
			alt = a[1]
		}
		return spell(alt, htmlImage.FindStringSubmatch(m)[1])
	})
}

// maxPicture bounds one picture the import downloads.
const maxPicture = 10 << 20

// exciterPictures attaches the pictures of an issue to its requirement and
// shows them in the statement. A requirement with attachments has them from
// an earlier run, or from somebody, and is left alone; a picture that cannot
// be had stays a link.
func (im *importer) exciterPictures(ctx context.Context, key string, i sampleIssue) {
	urls := pictureURLs(i.Body)
	if len(urls) == 0 {
		return
	}
	path := "/requirements/" + url.PathEscape(key)
	var list struct {
		Attachments []struct{} `json:"attachments"`
	}
	if err := im.exciter.send(ctx, http.MethodGet, path+"/attachments", nil, &list); err != nil {
		im.warn("%s shows its pictures as links: %v", key, err)
		return
	}
	if len(list.Attachments) > 0 {
		return
	}
	attached := map[string]string{}
	for n, u := range urls {
		content, err := im.picture(ctx, u)
		if err == nil {
			var made struct {
				Attachment struct {
					ID string `json:"id"`
				} `json:"attachment"`
			}
			err = im.exciter.upload(ctx, http.MethodPost, path+"/attachments", "file", fmt.Sprintf("picture-%d", n+1), content, &made)
			attached[u] = made.Attachment.ID
		}
		if err != nil {
			im.warn("%s shows the picture %s as a link: %v", key, u, err)
			delete(attached, u)
		}
	}
	if len(attached) == 0 {
		return
	}
	var current struct {
		Requirement struct {
			Revision int `json:"revision"`
		} `json:"requirement"`
	}
	err := im.exciter.send(ctx, http.MethodGet, path, nil, &current)
	if err == nil {
		body := map[string]any{"baseRevision": current.Requirement.Revision, "statementMarkdown": statementOf(i, attached)}
		err = im.exciter.send(ctx, http.MethodPatch, path, body, nil)
	}
	if err != nil {
		im.warn("%s has its pictures attached but shows them as links: %v", key, err)
		return
	}
	im.note("%s shows %d pictures of its issue", key, len(attached))
}

// picture is the picture at u, refused when it is not one or too large.
func (im *importer) picture(ctx context.Context, u string) ([]byte, error) {
	client := im.web
	if client == nil {
		client = &http.Client{Timeout: time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("it answered %s", resp.Status)
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, maxPicture+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxPicture {
		return nil, fmt.Errorf("it is larger than %d MB", maxPicture>>20)
	}
	if kind := http.DetectContentType(content); !strings.HasPrefix(kind, "image/") && !bytes.Contains(content[:min(len(content), 512)], []byte("<svg")) {
		return nil, fmt.Errorf("it is %s, not a picture", kind)
	}
	return content, nil
}

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlBreak   = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlTag     = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9-]*(\s[^<>]*)?/?>`)
)

// withoutHTML keeps a statement's text and drops its HTML, which Exciter
// refuses in a requirement; code, fenced or inline, stays as it is.
func withoutHTML(md string) string {
	md = htmlComment.ReplaceAllString(md, "")
	lines := strings.Split(md, "\n")
	fenced := false
	for i, line := range lines {
		if fenceLine.MatchString(line) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		parts := strings.Split(line, "`")
		for j := 0; j < len(parts); j += 2 {
			parts[j] = htmlTag.ReplaceAllString(htmlBreak.ReplaceAllString(parts[j], "  \n"), "")
		}
		lines[i] = strings.Join(parts, "`")
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "\n\n(cut short by the import)"
	}
	return s
}

// richText is Armature's description shape for plain text: a paragraph per
// block of lines, nil for none.
func richText(text string) map[string]any {
	var paragraphs []any
	for _, block := range strings.Split(strings.ReplaceAll(truncate(text, maxText), "\r\n", "\n"), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		paragraphs = append(paragraphs, map[string]any{
			"type":    "paragraph",
			"content": []any{map[string]any{"type": "text", "text": block}},
		})
	}
	if len(paragraphs) == 0 {
		return nil
	}
	return map[string]any{"type": "doc", "content": paragraphs}
}

// exciterAccounts connects Exciter's administrator to their Armature and
// Stator accounts, with which Exciter makes every trace link, unless they are
// connected already. The token is the administrator's in that tool, named
// dynamo-sample-data so it can be found and revoked.
func (im *importer) exciterAccounts(ctx context.Context) error {
	for _, t := range []struct {
		name string
		s    *session
	}{{"armature", im.armature}, {"stator", im.stator}} {
		var account struct {
			Account struct {
				Connected bool `json:"connected"`
			} `json:"account"`
		}
		if err := im.exciter.send(ctx, http.MethodGet, "/"+t.name+"/account", nil, &account); err != nil {
			return err
		}
		if account.Account.Connected {
			continue
		}
		var made struct {
			Token struct {
				Secret string `json:"secret"`
			} `json:"token"`
		}
		if err := t.s.send(ctx, http.MethodPost, "/tokens", map[string]string{"name": "dynamo-sample-data"}, &made); err != nil {
			return err
		}
		if err := im.exciter.send(ctx, http.MethodPut, "/"+t.name+"/account/token", map[string]string{"token": made.Token.Secret}, nil); err != nil {
			return fmt.Errorf("Exciter refused the %s token of its administrator: %w", t.name, err)
		}
		im.note("Exciter's administrator is connected to their %s account", t.name)
	}
	return nil
}
