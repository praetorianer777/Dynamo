package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

// sampleProject is one source's data, as GitHub or a snapshot gives it, before
// it becomes a project in each tool.
type sampleProject struct {
	// Repo is owner/name on GitHub; Key the project's key in every tool.
	Repo    string        `json:"repo"`
	Key     string        `json:"key"`
	Name    string        `json:"name"`
	Commit  string        `json:"commit"`
	License string        `json:"license"`
	Issues  []sampleIssue `json:"issues"`
	Docs    []sampleDoc   `json:"docs"`
	// Sites are documentation websites whose pages are among Docs, so links to them stay in Stator.
	Sites []docsSite `json:"sites,omitempty"`
}

// docsSite is a project's documentation website, built from the Markdown under
// Dir in Repo: a link to URL/<path> is the file Dir/<path>, give or take a
// language or version segment in front and the extension.
type docsSite struct {
	URL  string `json:"url"`
	Repo string `json:"repo"`
	Dir  string `json:"dir"`
	// Exclude are paths under Dir left out, such as an old version's docs.
	Exclude []string `json:"exclude,omitempty"`
}

// space is where the site's pages sit in the Stator space: under a page named after its host.
func (s docsSite) space() string {
	host := strings.TrimPrefix(strings.TrimPrefix(s.URL, "https://"), "http://")
	host, _, _ = strings.Cut(host, "/")
	return host
}

type sampleIssue struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Labels    []string `json:"labels"`
	Closed    bool     `json:"closed"`
	Milestone string   `json:"milestone,omitempty"`
}

// sampleDoc is a Markdown file, by its place in the Stator space: its path in
// the project's repository, or under the site's host for a docs site's page.
type sampleDoc struct {
	Path     string `json:"path"`
	Markdown string `json:"markdown"`
	// Repo and File are where a docs site's page comes from; empty for the project's own files.
	Repo string `json:"repo,omitempty"`
	File string `json:"file,omitempty"`
}

// origin is the repository and file a doc comes from.
func (p sampleProject) origin(d sampleDoc) (repo, file string) {
	if d.Repo == "" {
		return p.Repo, d.Path
	}
	return d.Repo, d.File
}

// own says whether a doc is the project's, whose #123 are the project's issues.
func (p sampleProject) own(d sampleDoc) bool { return d.Repo == "" || d.Repo == p.Repo }

// sourceKey names an item by where it came from, so a rerun finds what an
// earlier one made instead of making it twice.
func (p sampleProject) sourceKey(i sampleIssue) string {
	return fmt.Sprintf("%s#%d", p.Repo, i.Number)
}

// feature says whether an issue describes something to build, which Exciter
// takes as a requirement.
func (i sampleIssue) feature() bool {
	for _, l := range i.Labels {
		l = strings.ToLower(l)
		if strings.Contains(l, "feature") || strings.Contains(l, "enhancement") {
			return true
		}
	}
	return false
}

var bugLabel = regexp.MustCompile(`(?i)\bbug\b`)

// kind is the Armature issue type an issue becomes: Bug for a bug, Story for a feature, else Task.
func (i sampleIssue) kind() string {
	for _, l := range i.Labels {
		if bugLabel.MatchString(l) {
			return "Bug"
		}
	}
	if i.feature() {
		return "Story"
	}
	return "Task"
}

// title is a doc's front matter title, else its first heading, else its file
// name, or its folder's for a README or index.
func (d sampleDoc) title() string {
	meta, body := frontMatter(d.Markdown)
	if m := metaTitle.FindStringSubmatch(meta); m != nil && unquote(m[1]) != "" {
		return unquote(m[1])
	}
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		if fenceLine.MatchString(line) {
			fenced = !fenced
		}
		if t, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok && !fenced && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
	}
	name := path.Base(d.Path)
	if isIndex(d.Path) && folderOf(d.Path) != "" {
		name = path.Base(folderOf(d.Path))
	}
	return pretty(name)
}

var issueMention = regexp.MustCompile(`(?:^|[^\w&/])#(\d+)\b`)

// mentions are the issue numbers a doc names as #123, in order, once each.
func (d sampleDoc) mentions() []int { return issueMentions(d.Markdown) }

// mentions are the other issues an issue's body names.
func (i sampleIssue) mentions() []int {
	var out []int
	for _, n := range issueMentions(i.Body) {
		if n != i.Number {
			out = append(out, n)
		}
	}
	return out
}

func issueMentions(text string) []int {
	var out []int
	seen := map[int]bool{}
	for _, m := range issueMention.FindAllStringSubmatch(text, -1) {
		var n int
		fmt.Sscanf(m[1], "%d", &n)
		if n > 0 && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// sampleSource is one entry of sampleData.sources: a name from builtInSources,
// owner/repo, or either with =KEY to choose the project key.
type sampleSource struct {
	repo, key string
	site      *docsSite
}

// builtInSources are the names the chart documents; each key fits every
// tool's project keys (capitals, 2 to 10 long).
var builtInSources = map[string]sampleSource{
	"evcc":     {"evcc-io/evcc", "EVCC", &docsSite{URL: "https://docs.evcc.io", Repo: "evcc-io/docs", Dir: "src/content/docs/en"}},
	"armature": {"Cloudster1/Armature", "ARMAT", nil},
	"stator":   {"praetorianer777/Stator", "STATR", nil},
	"exciter":  {"Cloudster1/Exciter", "EXCIT", nil},
	"dynamo":   {"praetorianer777/Dynamo", "DYNAM", nil},
}

var projectKey = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

// sourceEntry is one source as DYNAMO_SAMPLE_SOURCES gives it: in JSON either
// a string as in the comma-separated form, or an object that may also name the
// project's docs site.
type sourceEntry struct {
	Repo string    `json:"repo"`
	Key  string    `json:"key"`
	Docs *docsSite `json:"docs"`
}

func (e *sourceEntry) UnmarshalJSON(b []byte) error {
	var name string
	if json.Unmarshal(b, &name) == nil {
		e.Repo, e.Key, _ = strings.Cut(strings.TrimSpace(name), "=")
		return nil
	}
	type plain sourceEntry
	return json.Unmarshal(b, (*plain)(e))
}

// sourceEntries reads a comma-separated list, or a JSON list when it starts with [.
func sourceEntries(v string) ([]sourceEntry, error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "[") {
		var out []sourceEntry
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return nil, fmt.Errorf("DYNAMO_SAMPLE_SOURCES starts with [ but is no JSON list of sources: %w", err)
		}
		return out, nil
	}
	var out []sourceEntry
	for _, entry := range strings.Split(v, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			name, key, _ := strings.Cut(entry, "=")
			out = append(out, sourceEntry{Repo: name, Key: key})
		}
	}
	return out, nil
}

func (e sourceEntry) source() (sampleSource, error) {
	name := e.Repo
	src, ok := builtInSources[strings.ToLower(name)]
	switch {
	case ok:
	case strings.Count(name, "/") == 1 && !strings.HasPrefix(name, "/") && !strings.HasSuffix(name, "/"):
		src = sampleSource{repo: name, key: keyFor(name)}
	default:
		return src, fmt.Errorf("sample data source %q is neither one of %s nor owner/repo", name, strings.Join(builtInNames(), ", "))
	}
	if e.Key != "" {
		src.key = e.Key
	}
	if !projectKey.MatchString(src.key) {
		return src, fmt.Errorf("sample data source %q gets the project key %q; give it one of 2 to 10 capitals and digits, starting with a capital, as %s=KEY", name, src.key, name)
	}
	if d := e.Docs; d != nil {
		if !strings.HasPrefix(d.URL, "https://") && !strings.HasPrefix(d.URL, "http://") || strings.Count(d.Repo, "/") != 1 {
			return src, fmt.Errorf("the docs of sample data source %q need url, the site's address, and repo, owner/repo of its sources, with dir the folder of its Markdown", name)
		}
		d.URL = strings.TrimRight(d.URL, "/")
		d.Dir = strings.Trim(d.Dir, "/")
		src.site = d
	}
	return src, nil
}

func parseSources(list string) ([]sampleSource, error) {
	entries, err := sourceEntries(list)
	if err != nil {
		return nil, err
	}
	var out []sampleSource
	for _, e := range entries {
		src, err := e.source()
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, nil
}

// keyFor makes a project key from a repository's name: its capitals and digits, at most ten.
func keyFor(repo string) string {
	name := strings.ToUpper(repo[strings.Index(repo, "/")+1:])
	var b strings.Builder
	for _, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9' && b.Len() > 0) {
			b.WriteRune(r)
		}
		if b.Len() == 10 {
			break
		}
	}
	return b.String()
}

func builtInNames() []string {
	return []string{"evcc", "armature", "stator", "exciter", "dynamo"}
}

//go:embed samples/*.json
var snapshots embed.FS

// readSnapshot reads a source written by writeSnapshot, or one built in by name.
func readSnapshot(r io.Reader) (sampleProject, error) {
	var p sampleProject
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return p, fmt.Errorf("read the sample data snapshot: %w", err)
	}
	if p.Repo == "" || !projectKey.MatchString(p.Key) {
		return p, fmt.Errorf("the sample data snapshot names repo %q and key %q; it needs both, the key 2 to 10 capitals and digits", p.Repo, p.Key)
	}
	return p, nil
}

func builtInSnapshot(name string) (sampleProject, error) {
	f, err := snapshots.Open("samples/" + name + ".json")
	if err != nil {
		return sampleProject{}, fmt.Errorf("there is no built-in snapshot %q", name)
	}
	defer f.Close()
	return readSnapshot(f)
}

func writeSnapshot(w io.Writer, p sampleProject) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}
