package main

import (
	"strings"
	"testing"
)

func TestAnMDXPageBecomesMarkdownHeadedByItsTitle(t *testing.T) {
	d := sampleDoc{Path: "docs.tool.example/features/solar.mdx", Markdown: "---\ntitle: 'Solar surplus'\nsidebar: 3\n---\nimport Tabs from '@theme/Tabs';\n\n<Tabs>\nCharge from the sun.\n</Tabs>\n\n```js\nimport x from 'y'\n<Tabs>\n```\n"}
	if got := d.title(); got != "Solar surplus" {
		t.Fatalf("title %q", got)
	}
	got := cleanMarkdown(d.Markdown, d.title())
	want := "# Solar surplus\n\nCharge from the sun.\n\n```js\nimport x from 'y'\n<Tabs>\n```\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if (sampleDoc{Path: "docs/setup/README.md", Markdown: "no heading"}).title() != "Setup" {
		t.Error("a README without a heading is not named after its folder")
	}
}

func treeProject() sampleProject {
	return sampleProject{Repo: "o/tool", Name: "tool",
		Sites: []docsSite{{URL: "https://docs.tool.example", Repo: "o/tool-docs", Dir: "content/en"}},
		Docs: []sampleDoc{
			{Path: "README.md", Markdown: "# Tool"},
			{Path: "CHANGELOG.md", Markdown: "# Changelog"},
			{Path: "docs/setup/install.md", Markdown: "# Install"},
			{Path: "docs/faq.md", Markdown: "# FAQ"},
			{Path: "docs/setup/README.md", Markdown: "# Setting up"},
			{Path: "docs.tool.example/index.mdx", Repo: "o/tool-docs", File: "content/en/index.mdx", Markdown: "# Docs"},
			{Path: "docs.tool.example/features/solar.mdx", Repo: "o/tool-docs", File: "content/en/features/solar.mdx", Markdown: "# Solar"},
			{Path: "docs.tool.example/features/battery.md", Repo: "o/tool-docs", File: "content/en/features/battery.md", Markdown: "# Battery"},
		}}
}

func TestTheDocsBecomeATreeOfPages(t *testing.T) {
	var got []string
	for _, n := range pageTree(treeProject()) {
		got = append(got, n.key+"<"+n.parent+":"+n.title)
	}
	want := "<:Tool docs<:Docs docs.tool.example<:Docs CHANGELOG.md<:Changelog docs.tool.example/features<docs.tool.example:Features docs/faq.md<docs:FAQ docs/setup<docs:Setting up docs.tool.example/features/battery.md<docs.tool.example/features:Battery docs.tool.example/features/solar.mdx<docs.tool.example/features:Solar docs/setup/install.md<docs/setup:Install"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
	for _, n := range pageTree(treeProject()) {
		switch n.key {
		case "docs":
			if n.source != "https://github.com/o/tool/tree/HEAD/docs" || n.doc != nil {
				t.Errorf("the folder docs is %+v", n)
			}
		case "docs.tool.example/features":
			if n.source != "https://github.com/o/tool-docs/tree/HEAD/content/en/features" {
				t.Errorf("the site's folder links to %s", n.source)
			}
		case "docs.tool.example/features/solar.mdx":
			if n.source != "https://github.com/o/tool-docs/blob/HEAD/content/en/features/solar.mdx" {
				t.Errorf("the site's page links to %s", n.source)
			}
		}
	}
}

func TestLinksLeadToWhatWasImported(t *testing.T) {
	p := treeProject()
	l := newLinker(p, func(key string) (string, bool) { return "https://stator/p/" + strings.ReplaceAll(key, "/", "_"), true },
		map[int]string{7: "https://armature/issues/TOOL-3"})
	readme, site := p.Docs[0], p.Docs[6]
	for _, c := range []struct {
		doc          sampleDoc
		target, want string
	}{
		{readme, "docs/setup/install.md#linux", "https://stator/p/docs_setup_install.md#linux"},
		{readme, "./docs/setup/", "https://stator/p/docs_setup"},
		{readme, "https://github.com/o/tool/blob/main/CHANGELOG.md", "https://stator/p/CHANGELOG.md"},
		{readme, "https://github.com/o/tool/issues/7", "https://armature/issues/TOOL-3"},
		{readme, "https://github.com/o/tool/issues/8", "https://github.com/o/tool/issues/8"},
		{readme, "https://docs.tool.example/en/docs/features/solar", "https://stator/p/docs.tool.example_features_solar.mdx"},
		{readme, "https://docs.tool.example/", "https://stator/p/docs.tool.example"},
		{readme, "https://docs.tool.example/en/docs/devices/chargers", "https://docs.tool.example/en/docs/devices/chargers"},
		{readme, "docs/logo.png", "https://github.com/o/tool/raw/HEAD/docs/logo.png"},
		{readme, "LICENSE", "https://github.com/o/tool/blob/HEAD/LICENSE"},
		{readme, "#usage", "#usage"},
		{site, "../index.mdx", "https://stator/p/docs.tool.example"},
		{site, "/en/features/solar/", "https://stator/p/docs.tool.example_features_solar.mdx"},
	} {
		repo, file := p.origin(c.doc)
		if got := l.resolve(repo, file, c.target); got != c.want {
			t.Errorf("%s in %s leads to %s, want %s", c.target, c.doc.Path, got, c.want)
		}
	}

	md := "See [install](docs/setup/install.md \"how\") and <a href=\"CHANGELOG.md\">news</a>.\n\n[ref]: https://github.com/o/tool/issues/7\n\n```\n[code](docs/setup/install.md)\n```\n"
	want := "See [install](https://stator/p/docs_setup_install.md \"how\") and <a href=\"https://stator/p/CHANGELOG.md\">news</a>.\n\n[ref]: https://armature/issues/TOOL-3\n\n```\n[code](docs/setup/install.md)\n```\n"
	if got := l.rewrite(readme, md); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestAFolderOfOnePageIsLeftOut(t *testing.T) {
	p := sampleProject{Repo: "o/tool", Name: "tool", Docs: []sampleDoc{
		{Path: "README.md", Markdown: "# Tool"},
		{Path: "docs/blog/2025/01/20/news.md", Markdown: "# News"},
		{Path: "docs/blog/2025/02/03/more.md", Markdown: "# More"},
	}}
	var got []string
	for _, n := range pageTree(p) {
		got = append(got, n.key+"<"+n.parent)
	}
	if want := "< docs/blog/2025< docs/blog/2025/01/20/news.md<docs/blog/2025 docs/blog/2025/02/03/more.md<docs/blog/2025"; strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}
