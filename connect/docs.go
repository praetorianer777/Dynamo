package main

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// docExtensions are the files a doc is read from.
var docExtensions = []string{".md", ".mdx"}

func isDoc(file string) bool {
	lower := strings.ToLower(file)
	for _, ext := range docExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func withoutExt(file string) string {
	for _, ext := range docExtensions {
		if strings.HasSuffix(strings.ToLower(file), ext) {
			return file[:len(file)-len(ext)]
		}
	}
	return file
}

// isIndex says whether a doc is its folder's own page, as README.md or index.md are.
func isIndex(p string) bool {
	switch strings.ToLower(withoutExt(path.Base(p))) {
	case "readme", "index", "_index":
		return true
	}
	return false
}

// frontMatter splits off a leading YAML block between --- lines.
func frontMatter(md string) (meta, body string) {
	if !strings.HasPrefix(md, "---\n") && !strings.HasPrefix(md, "---\r\n") {
		return "", md
	}
	rest := md[strings.Index(md, "\n")+1:]
	for i := 0; i < len(rest); {
		end := strings.IndexByte(rest[i:], '\n')
		line := rest[i:]
		if end >= 0 {
			line = rest[i : i+end]
		}
		if strings.TrimSpace(line) == "---" {
			if end < 0 {
				return rest[:i], ""
			}
			return rest[:i], rest[i+end+1:]
		}
		if end < 0 {
			break
		}
		i += end + 1
	}
	return "", md
}

var metaTitle = regexp.MustCompile(`(?m)^title:\s*(.+?)\s*$`)

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		if u, err := strconv.Unquote(`"` + s[1:len(s)-1] + `"`); err == nil {
			return u
		}
		return s[1 : len(s)-1]
	}
	return s
}

// pretty makes a page title from a file or folder name.
func pretty(name string) string {
	name = strings.NewReplacer("-", " ", "_", " ").Replace(withoutExt(name))
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

var (
	mdxLine   = regexp.MustCompile(`^\s*(import\s.+\sfrom\s|export\s|</?[A-Z][\w.]*(\s[^>]*)?/?>\s*$)`)
	fenceLine = regexp.MustCompile("^\\s*(```|~~~)")
)

// cleanMarkdown is a doc as Stator imports it: without front matter, without
// MDX's imports and component lines, which Stator would show as text, and
// headed by its title, which Stator takes as the page's.
func cleanMarkdown(md, title string) string {
	_, body := frontMatter(md)
	var out []string
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		if fenceLine.MatchString(line) {
			fenced = !fenced
		}
		if !fenced && mdxLine.MatchString(line) {
			continue
		}
		out = append(out, line)
	}
	body = strings.TrimLeft(strings.Join(out, "\n"), "\n")
	if !strings.HasPrefix(body, "# ") {
		body = "# " + title + "\n\n" + body
	}
	return body
}

// pageNode is one page of a sample's space: a doc, or a folder that has no
// doc of its own, which lists the pages under it.
type pageNode struct {
	// key is the doc's place, or the folder's for a folder's page; "" is the space's home.
	key, parent string
	title       string
	// source is the GitHub address of the doc or folder, which the page links
	// to and by which a rerun finds it.
	source string
	doc    *sampleDoc
}

func folderOf(key string) string {
	if dir := path.Dir(key); dir != "." {
		return dir
	}
	return ""
}

// pageTree is the space's pages, each after the page it sits under.
func pageTree(p sampleProject) []pageNode {
	nodes := map[string]*pageNode{}
	sites := map[string]docsSite{}
	for _, s := range p.Sites {
		sites[s.space()] = s
	}
	for i := range p.Docs {
		d := &p.Docs[i]
		key := d.Path
		if isIndex(d.Path) {
			key = folderOf(d.Path)
		}
		if _, taken := nodes[key]; taken {
			continue
		}
		repo, file := p.origin(*d)
		nodes[key] = &pageNode{key: key, title: d.title(), doc: d,
			source: fmt.Sprintf("https://github.com/%s/blob/HEAD/%s", repo, file)}
	}
	for _, key := range keysOf(nodes) {
		for dir := folderOf(key); ; dir = folderOf(dir) {
			if _, ok := nodes[dir]; ok || dir == "" {
				break
			}
			top, rest, _ := strings.Cut(dir, "/")
			repo, folder, title := p.Repo, dir, pretty(path.Base(dir))
			if s, ok := sites[top]; ok {
				repo, folder = s.Repo, strings.TrimPrefix(path.Join(s.Dir, rest), "/")
				if rest == "" {
					title = top
				}
			}
			nodes[dir] = &pageNode{key: dir, title: title, source: fmt.Sprintf("https://github.com/%s/tree/HEAD/%s", repo, folder)}
		}
	}
	if _, ok := nodes[""]; !ok {
		nodes[""] = &pageNode{key: "", title: p.Name, source: "https://github.com/" + p.Repo + "/blob/HEAD/README.md"}
	}
	parentOf := func(key string) string {
		for dir := folderOf(key); ; dir = folderOf(dir) {
			if _, ok := nodes[dir]; ok || dir == "" {
				return dir
			}
		}
	}
	// A folder without a doc of its own that holds a single page, as a blog's
	// date folders do, would only be one more click: its page moves up instead.
	for changed := true; changed; {
		changed = false
		children := map[string]int{}
		for key := range nodes {
			if key != "" {
				children[parentOf(key)]++
			}
		}
		for _, key := range keysOf(nodes) {
			if n := nodes[key]; key != "" && n.doc == nil && children[key] == 1 {
				delete(nodes, key)
				changed = true
				break
			}
		}
	}
	var out []pageNode
	for _, key := range keysOf(nodes) {
		n := *nodes[key]
		if key != "" {
			n.parent = parentOf(key)
		}
		out = append(out, n)
	}
	// The home first, then each level after the one above it, so a page's parent is always made first.
	sort.SliceStable(out, func(a, b int) bool {
		da, db := depth(out[a].key), depth(out[b].key)
		if da != db {
			return da < db
		}
		return order(out[a]) < order(out[b])
	})
	return out
}

func depth(key string) int {
	if key == "" {
		return -1
	}
	return strings.Count(key, "/")
}

// order sorts pages by path, a CHANGELOG after its siblings.
func order(n pageNode) string {
	name := strings.ToLower(n.key)
	if strings.HasPrefix(name, "changelog") {
		return "~" + name
	}
	return name
}

func keysOf(m map[string]*pageNode) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// linker rewrites a doc's links to what was imported: other docs to their
// Stator pages, the project's issues to Armature, and the rest of the
// repository to GitHub, since a relative link means nothing in Stator.
type linker struct {
	p      sampleProject
	pages  map[string]string // repo + "/" + file, without extension, to the page's address
	issues map[int]string    // issue number to the Armature issue's address
}

func newLinker(p sampleProject, pageURL func(key string) (string, bool), issueURL map[int]string) *linker {
	l := &linker{p: p, pages: map[string]string{}, issues: issueURL}
	for _, d := range p.Docs {
		key := d.Path
		if isIndex(d.Path) {
			key = folderOf(d.Path)
		}
		address, ok := pageURL(key)
		if !ok {
			continue
		}
		repo, file := p.origin(d)
		l.pages[repo+"/"+withoutExt(file)] = address
		if isIndex(file) {
			l.pages[repo+"/"+folderOf(file)] = address
		}
	}
	return l
}

var (
	inlineLink = regexp.MustCompile(`\]\(\s*<?([^()\s<>]+)>?((?:\s+"[^"]*")?\s*)\)`)
	refLink    = regexp.MustCompile(`(?m)^(\s{0,3}\[[^\]]+\]:\s*)<?(\S+?)>?(\s|$)`)
	htmlLink   = regexp.MustCompile(`(\s(?:href|src)=")([^"]+)(")`)
	githubPath = regexp.MustCompile(`^https://github\.com/([^/]+/[^/]+)/(?:blob|tree|raw)/[^/]+/(.+)$`)
	githubIss  = regexp.MustCompile(`^https://github\.com/([^/]+/[^/]+)/(?:issues|pull)/(\d+)/?$`)
)

// rewrite is a doc's Markdown with its links rewritten, code left alone.
func (l *linker) rewrite(d sampleDoc, md string) string {
	repo, file := l.p.origin(d)
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
		fix := func(target string) string { return l.resolve(repo, file, target) }
		line = inlineLink.ReplaceAllStringFunc(line, func(m string) string {
			sub := inlineLink.FindStringSubmatch(m)
			return "](" + fix(sub[1]) + sub[2] + ")"
		})
		line = refLink.ReplaceAllStringFunc(line, func(m string) string {
			sub := refLink.FindStringSubmatch(m)
			return sub[1] + fix(sub[2]) + sub[3]
		})
		line = htmlLink.ReplaceAllStringFunc(line, func(m string) string {
			sub := htmlLink.FindStringSubmatch(m)
			return sub[1] + fix(sub[2]) + sub[3]
		})
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// resolve is where a link of the file in repo should lead.
func (l *linker) resolve(repo, file, target string) string {
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
		return target
	}
	bare, fragment, _ := strings.Cut(target, "#")
	if fragment != "" {
		fragment = "#" + fragment
	}
	if m := githubIss.FindStringSubmatch(bare); m != nil && strings.EqualFold(m[1], l.p.Repo) {
		n, _ := strconv.Atoi(m[2])
		if address, ok := l.issues[n]; ok {
			return address
		}
		return target
	}
	if m := githubPath.FindStringSubmatch(bare); m != nil {
		if address, ok := l.page(m[1], m[2]); ok {
			return address + fragment
		}
		return target
	}
	if u, err := url.Parse(bare); err == nil && u.Scheme != "" {
		if address, ok := l.sitePage(bare); ok {
			return address + fragment
		}
		return target
	}
	if bare == "" {
		return target
	}
	if strings.HasPrefix(bare, "/") {
		for _, s := range l.p.Sites {
			if s.Repo == repo {
				if address, ok := l.sitePage(s.URL + bare); ok {
					return address + fragment
				}
			}
		}
	}
	resolved := path.Clean(path.Join(path.Dir(file), bare))
	if strings.HasPrefix(bare, "/") {
		resolved = strings.TrimPrefix(path.Clean(bare), "/")
	}
	if address, ok := l.page(repo, resolved); ok {
		return address + fragment
	}
	if strings.HasPrefix(resolved, "../") || resolved == ".." {
		return target
	}
	kind := "blob"
	if isImage(resolved) {
		kind = "raw"
	}
	return fmt.Sprintf("https://github.com/%s/%s/HEAD/%s%s", repo, kind, resolved, fragment)
}

func isImage(file string) bool {
	switch strings.ToLower(path.Ext(file)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp":
		return true
	}
	return false
}

// page is the page of the file in repo, with or without its extension, or of the folder.
func (l *linker) page(repo, file string) (string, bool) {
	file = strings.TrimSuffix(file, "/")
	for _, k := range []string{withoutExt(file), file, strings.TrimSuffix(file, ".html")} {
		if address, ok := l.pages[repo+"/"+k]; ok {
			return address, true
		}
	}
	return "", false
}

// sitePage is the page a docs site's address shows, found by its path below
// the site's folder, dropping language or version segments in front.
func (l *linker) sitePage(address string) (string, bool) {
	for _, s := range l.p.Sites {
		rest, ok := strings.CutPrefix(address, s.URL)
		if !ok || (rest != "" && rest[0] != '/') {
			continue
		}
		rest = strings.Trim(strings.TrimSuffix(strings.Trim(rest, "/"), ".html"), "/")
		segs := strings.Split(rest, "/")
		for i := 0; i < len(segs); i++ {
			if found, ok := l.page(s.Repo, path.Join(s.Dir, strings.Join(segs[i:], "/"))); ok {
				return found, true
			}
		}
	}
	return "", false
}
