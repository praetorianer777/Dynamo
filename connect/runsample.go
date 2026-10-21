package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// sampleConfig is the sample-data mode's own part of the environment.
type sampleConfig struct {
	sources   []sampleSource
	snapshots []string
	maxIssues int
	token     string
}

// loadSample reads DYNAMO_SAMPLE_SOURCES, where a built-in snapshot's name
// (sparkplug) needs no network and anything else is read from GitHub.
func loadSample(getenv func(string) string) (sampleConfig, error) {
	cfg := sampleConfig{maxIssues: 500, token: getenv("GITHUB_TOKEN")}
	if v := getenv("DYNAMO_SAMPLE_MAX_ISSUES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return cfg, fmt.Errorf("DYNAMO_SAMPLE_MAX_ISSUES is a number of issues, 0 for all, not %q", v)
		}
		cfg.maxIssues = n
	}
	entries, err := sourceEntries(getenv("DYNAMO_SAMPLE_SOURCES"))
	if err != nil {
		return cfg, err
	}
	for _, e := range entries {
		if _, err := builtInSnapshot(e.Repo); err == nil && e.Key == "" && e.Docs == nil {
			cfg.snapshots = append(cfg.snapshots, e.Repo)
			continue
		}
		src, err := e.source()
		if err != nil {
			return cfg, err
		}
		cfg.sources = append(cfg.sources, src)
	}
	if len(cfg.sources)+len(cfg.snapshots) == 0 {
		return cfg, fmt.Errorf("DYNAMO_SAMPLE_SOURCES names no source; give one or more of sparkplug, %s, or owner/repo", strings.Join(builtInNames(), ", "))
	}
	return cfg, nil
}

// runSample fills the tools with each source, after they answer and once
// dynamo-connect connected them.
func runSample(ctx context.Context, cfg config, sc sampleConfig) ([]string, []string, error) {
	var changes, warnings []string
	im := &importer{
		armatureURL: strings.TrimRight(cfg.armature.url, "/"),
		statorURL:   strings.TrimRight(cfg.stator.url, "/"),
		exciterURL:  strings.TrimRight(cfg.exciter.url, "/"),
		note:        func(f string, a ...any) { changes = append(changes, fmt.Sprintf(f, a...)) },
		warn:        func(f string, a ...any) { warnings = append(warnings, fmt.Sprintf(f, a...)) },
	}
	for _, t := range []tool{cfg.stator, cfg.armature, cfg.exciter} {
		if err := awaitMeta(ctx, t, cfg.waitFor); err != nil {
			return changes, warnings, err
		}
	}
	for _, s := range []struct {
		s **session
		t tool
	}{{&im.armature, cfg.armature}, {&im.stator, cfg.stator}, {&im.exciter, cfg.exciter}} {
		*s.s = newSession(s.t.product, s.t.api)
		if err := (*s.s).signIn(ctx, s.t.email, s.t.password); err != nil {
			return changes, warnings, err
		}
	}
	if err := im.exciterAccounts(ctx); err != nil {
		return changes, warnings, err
	}

	var projects []sampleProject
	for _, name := range sc.snapshots {
		p, err := builtInSnapshot(name)
		if err != nil {
			return changes, warnings, err
		}
		projects = append(projects, p)
	}
	gh := newGitHub(sc.token)
	for _, src := range sc.sources {
		slog.Info("reading the sample source", "repo", src.repo, "maxIssues", sc.maxIssues)
		p, err := gh.project(ctx, src, sc.maxIssues)
		if err != nil {
			return changes, warnings, err
		}
		projects = append(projects, p)
	}
	for _, p := range projects {
		slog.Info("importing the sample", "repo", p.Repo, "key", p.Key, "issues", len(p.Issues), "docs", len(p.Docs))
		if err := im.project(ctx, p); err != nil {
			return changes, warnings, fmt.Errorf("import %s: %w", p.Repo, err)
		}
	}
	return changes, warnings, nil
}
