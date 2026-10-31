//go:build stack

// The two halves of make test-upgrade and make test-chart-upgrade, which
// scripts/upgrade-test.sh and scripts/chart-upgrade-test.sh run on either side
// of the upgrade: the first gives people different access to the sample at
// the previous versions and records what they and the sample are, the second
// holds the upgraded stack to that record. Both skip unless
// DYNAMO_UPGRADE_SNAPSHOT names the record's file.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
)

// upgradePerson is one of the demo realm's people and what the seed makes of them.
type upgradePerson struct{ name, email, password, access string }

// The people of deploy/keycloak/realm.json; scripts/upgrade-test.sh lets
// alice, bob and carol in by DYNAMO_MEMBERS, scripts/chart-upgrade-values.yaml
// by members, and dave comes in by his group.
var upgradePeople = []upgradePerson{
	{"alice", "alice@suite.test", "alice password", "an administrator of all three"},
	{"bob", "bob@suite.test", "bob password", "a member who edits the sample"},
	{"carol", "carol@suite.test", "carol password", "a member who only reads the sample"},
	{"dave", "dave@suite.test", "dave password", "let in and given the sample through the realm group marketing"},
}

const (
	upgradeProject = "SPARK"
	upgradeGroup   = "marketing"
)

func upgradeSnapshotFile(t *testing.T) string {
	t.Helper()
	file := os.Getenv("DYNAMO_UPGRADE_SNAPSHOT")
	if file == "" {
		t.Skip("DYNAMO_UPGRADE_SNAPSHOT is not set; make test-upgrade runs this")
	}
	return file
}

// TestUpgradeSeedsThePreviousVersions gives each person their access to the
// sample, which sample-data imported, and records what everyone may do.
func TestUpgradeSeedsThePreviousVersions(t *testing.T) {
	file := upgradeSnapshotFile(t)
	env := stackEnv(t)
	ctx := context.Background()
	admins := upgradeAdmins(t, ctx, env)

	// Before dave's first sign-in: Stator and Exciter let somebody new in
	// then whose provider group already holds a grant, Armature only
	// somebody it knows by address.
	for _, product := range []string{"stator", "exciter"} {
		letGroupIn(t, ctx, admins[product])
	}
	dave := upgradePeople[3]
	err := admins["armature"].send(ctx, http.MethodPost, "/users", map[string]string{"email": dave.email, "name": dave.name, "role": "member", "password": randomPassword()}, nil)
	if err != nil && !hasStatus(err, http.StatusConflict) {
		t.Fatalf("let %s into Armature: %v", dave.email, err)
	}

	// Signing in makes each person's account and the realm's groups in each tool.
	browsers := signInEveryone(t, env)

	for _, product := range []string{"stator", "exciter"} {
		grantPeople(t, ctx, admins[product])
	}
	grantArmature(t, ctx, admins["armature"])

	content, refs := collectContent(t, ctx, admins)
	access := readAccess(t, env, browsers, refs)
	checkSeeded(t, access)

	snapshot := upgradeSnapshot{
		Versions:            versionsOf(t, ctx, admins),
		Content:             content,
		Access:              access,
		ArmaturePermissions: armaturePermissions(t, ctx, admins["armature"]),
		Refs:                refs,
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatalf("keep the record for after the upgrade: %v", err)
	}
	for _, kind := range slices.Sorted(maps.Keys(content)) {
		t.Logf("%s: %d", kind, len(content[kind]))
	}
	t.Logf("recorded at %v what %d people may do, in %s", snapshot.Versions, len(access), file)
}

// TestUpgradeKeepsWhatWasThere checks the upgraded stack against the record:
// the sample and its links are there and resolve, everybody signs in through
// the realm and may do exactly what they could.
func TestUpgradeKeepsWhatWasThere(t *testing.T) {
	file := upgradeSnapshotFile(t)
	raw, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s is missing; TestUpgradeSeedsThePreviousVersions records it before the upgrade", file)
	}
	if err != nil {
		t.Fatal(err)
	}
	var before upgradeSnapshot
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatalf("%s is not the record TestUpgradeSeedsThePreviousVersions writes: %v", file, err)
	}
	env := stackEnv(t)
	ctx := context.Background()
	admins := upgradeAdmins(t, ctx, env)
	t.Logf("upgraded from %v to %v", before.Versions, versionsOf(t, ctx, admins))

	content, _ := collectContent(t, ctx, admins)
	for _, kind := range slices.Sorted(maps.Keys(before.Content)) {
		if gone := lost(before.Content[kind], content[kind]); len(gone) > 0 {
			t.Errorf("%s: the upgrade lost %d of %d:\n  %s", kind, len(gone), len(before.Content[kind]), strings.Join(gone, "\n  "))
		}
	}

	browsers := signInEveryone(t, env)
	after := readAccess(t, env, browsers, before.Refs)
	known := map[string]bool{}
	for _, p := range armaturePermissions(t, ctx, admins["armature"]) {
		if slices.Contains(before.ArmaturePermissions, p) {
			known[p] = true
		}
	}
	for _, p := range upgradePeople {
		for _, what := range slices.Sorted(maps.Keys(before.Access[p.name])) {
			now, ok := after[p.name][what]
			if !ok {
				t.Errorf("%s (%s): %s was not asked after the upgrade", p.name, p.access, what)
				continue
			}
			var vocabulary map[string]bool
			if strings.HasPrefix(what, "Armature:") {
				vocabulary = known
			}
			for _, change := range readingChanges(before.Access[p.name][what], now, vocabulary) {
				t.Errorf("%s (%s): %s: %s", p.name, p.access, what, change)
			}
		}
	}

	linksResolve(t, ctx, admins, env, before.Content)
}

// upgradeAdmins signs each tool's administrator in by password, as the stack checks do.
func upgradeAdmins(t *testing.T, ctx context.Context, env map[string]string) map[string]*session {
	t.Helper()
	out := map[string]*session{}
	for _, product := range products {
		P := strings.ToUpper(product)
		s := newSession(product, env[P+"_URL"])
		s.client = browser()
		if err := s.signIn(ctx, env[P+"_ADMIN_EMAIL"], env[P+"_ADMIN_PASSWORD"]); err != nil {
			t.Fatal(err)
		}
		out[product] = s
	}
	return out
}

// signInEveryone signs each person in to all three through the realm, each
// in a browser of their own, which their reads then go through.
func signInEveryone(t *testing.T, env map[string]string) map[string]*http.Client {
	t.Helper()
	out := map[string]*http.Client{}
	for _, p := range upgradePeople {
		b := browser()
		for i, product := range products {
			signInThroughRealm(t, b, product, env[strings.ToUpper(product)+"_URL"], p.email, p.password, i == 0)
		}
		out[p.name] = b
	}
	return out
}

func versionsOf(t *testing.T, ctx context.Context, admins map[string]*session) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, product := range products {
		var m meta
		if err := admins[product].send(ctx, http.MethodGet, "/meta", nil, &m); err != nil {
			t.Fatal(err)
		}
		out[product] = m.Build.Version
	}
	return out
}

func armaturePermissions(t *testing.T, ctx context.Context, s *session) []string {
	t.Helper()
	var got struct {
		Permissions []struct {
			Permission string `json:"permission"`
		} `json:"permissions"`
	}
	if err := s.send(ctx, http.MethodGet, "/permissions", nil, &got); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range got.Permissions {
		out = append(out, p.Permission)
	}
	return out
}

// sampleRoles is how Stator or Exciter names the sample in a grant and in
// the list of its grants, and the role each of the seed's subjects holds there.
type sampleRoles struct {
	field, filter     string
	bob, carol, group string
}

// upgradeRoles: bob edits, carol reads, the realm group reads and comments,
// and Everyone reads, in Stator's space and Exciter's project.
var upgradeRoles = map[string]sampleRoles{
	"stator":  {"spaceKey", "space", "editor", "viewer", "commenter"},
	"exciter": {"projectKey", "project", "editor", "viewer", "reviewer"},
}

// letGroupIn makes the realm group's people members at their first sign-in,
// through a group carrying its claim that holds its role in the sample.
func letGroupIn(t *testing.T, ctx context.Context, s *session) {
	t.Helper()
	var current struct {
		Provider *struct {
			Issuer       string `json:"issuer"`
			ClientID     string `json:"clientId"`
			GroupsClaim  string `json:"groupsClaim"`
			Scopes       string `json:"scopes"`
			CreateGroups bool   `json:"createGroups"`
			Enabled      bool   `json:"enabled"`
		} `json:"provider"`
	}
	if err := s.send(ctx, http.MethodGet, "/oidc-provider", nil, &current); err != nil {
		t.Fatal(err)
	}
	p := current.Provider
	if p == nil {
		t.Fatalf("%s has no identity provider; dynamo-connect points it at the realm before this runs", displayName[s.product])
	}
	// The client secret, left out, stays the one dynamo-connect set.
	body := map[string]any{
		"issuer": p.Issuer, "clientId": p.ClientID, "groupsClaim": p.GroupsClaim, "scopes": p.Scopes,
		"createGroups": p.CreateGroups, "enabled": p.Enabled, "admitByGroup": true,
	}
	if err := s.send(ctx, http.MethodPut, "/oidc-provider", body, nil); err != nil {
		t.Fatalf("let %s admit somebody new by their group: %v", displayName[s.product], err)
	}
	group := findGroup(t, ctx, s, "/groups?q="+upgradeGroup, func(g toolGroup) bool { return g.Name == upgradeGroup })
	if group == "" {
		var made struct {
			Group toolGroup `json:"group"`
		}
		if err := s.send(ctx, http.MethodPost, "/groups", map[string]string{"name": upgradeGroup, "externalRef": upgradeGroup}, &made); err != nil {
			t.Fatalf("make %s's group for the realm group %s: %v", displayName[s.product], upgradeGroup, err)
		}
		group = made.Group.ID
	}
	grantSample(t, ctx, s, upgradeRoles[s.product].group, nil, group)
}

// grantPeople gives bob and carol their roles in the sample, and Everyone
// reading it instead of the editing the tool gave every member when it was made.
func grantPeople(t *testing.T, ctx context.Context, s *session) {
	t.Helper()
	roles := upgradeRoles[s.product]
	bob, carol := memberIDs(t, ctx, s)
	grantSample(t, ctx, s, roles.bob, bob, nil)
	grantSample(t, ctx, s, roles.carol, carol, nil)
	everyone := findGroup(t, ctx, s, "/groups?limit=50", func(g toolGroup) bool { return g.Builtin == "everyone" })
	if everyone == "" {
		t.Fatalf("%s lists no group Everyone, which it keeps itself", displayName[s.product])
	}
	grantSample(t, ctx, s, "viewer", nil, everyone)
	var held struct {
		Assignments []struct {
			ID           string `json:"id"`
			Role         string `json:"role"`
			GroupBuiltin string `json:"groupBuiltin"`
		} `json:"assignments"`
	}
	if err := s.send(ctx, http.MethodGet, "/role-assignments?"+roles.filter+"="+upgradeProject+"&limit=200", nil, &held); err != nil {
		t.Fatal(err)
	}
	for _, a := range held.Assignments {
		if a.GroupBuiltin != "everyone" || a.Role == "viewer" {
			continue
		}
		if err := s.send(ctx, http.MethodDelete, "/role-assignments/"+a.ID, nil, nil); err != nil {
			t.Fatalf("take %s from Everyone in %s's %s: %v", a.Role, displayName[s.product], upgradeProject, err)
		}
	}
}

// grantSample grants a role in the sample to a person or a group, which may
// hold it already.
func grantSample(t *testing.T, ctx context.Context, s *session, role string, user, group any) {
	t.Helper()
	body := map[string]any{upgradeRoles[s.product].field: upgradeProject, "role": role, "userId": user, "groupId": group}
	if err := s.send(ctx, http.MethodPost, "/role-assignments", body, nil); err != nil && !hasStatus(err, http.StatusConflict) {
		t.Fatalf("grant %s in %s's %s: %v", role, displayName[s.product], upgradeProject, err)
	}
}

// memberIDs answers the ids Stator or Exciter gave bob and carol.
func memberIDs(t *testing.T, ctx context.Context, s *session) (bob, carol string) {
	t.Helper()
	var members struct {
		Members []struct {
			Email  string `json:"email"`
			UserID string `json:"userId"`
		} `json:"members"`
	}
	if err := s.send(ctx, http.MethodGet, "/users", nil, &members); err != nil {
		t.Fatal(err)
	}
	for _, m := range members.Members {
		switch m.Email {
		case upgradePeople[1].email:
			bob = m.UserID
		case upgradePeople[2].email:
			carol = m.UserID
		}
	}
	if bob == "" || carol == "" {
		t.Fatalf("%s lists no account for bob or carol after they signed in", displayName[s.product])
	}
	return bob, carol
}

type toolGroup struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Builtin string `json:"builtin"`
}

// findGroup answers the id of the first group the list at path holds that
// is, or "" when there is none.
func findGroup(t *testing.T, ctx context.Context, s *session, path string, is func(toolGroup) bool) string {
	t.Helper()
	var groups struct {
		Groups []toolGroup `json:"groups"`
	}
	if err := s.send(ctx, http.MethodGet, path, nil, &groups); err != nil {
		t.Fatal(err)
	}
	for _, g := range groups.Groups {
		if is(g) {
			return g.ID
		}
	}
	return ""
}

func groupID(t *testing.T, ctx context.Context, s *session, path string) string {
	t.Helper()
	id := findGroup(t, ctx, s, path, func(g toolGroup) bool { return g.Name == upgradeGroup })
	if id == "" {
		t.Fatalf("%s has no group %s after dave signed in; it makes the realm's groups at sign-in when createGroups is on", displayName[s.product], upgradeGroup)
	}
	return id
}

// grantArmature grants Armature's roles in the project: bob is a user, carol
// and the group readers.
func grantArmature(t *testing.T, ctx context.Context, s *session) {
	t.Helper()
	ids := map[string]string{}
	for _, p := range upgradePeople[1:3] {
		var users struct {
			Users []struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"users"`
		}
		if err := s.send(ctx, http.MethodGet, "/users?q="+url.QueryEscape(p.email), nil, &users); err != nil {
			t.Fatal(err)
		}
		for _, u := range users.Users {
			if u.Email == p.email {
				ids[p.name] = u.ID
			}
		}
		if ids[p.name] == "" {
			t.Fatalf("Armature lists no account for %s after they signed in", p.email)
		}
	}
	group := groupID(t, ctx, s, "/groups")
	for _, g := range []struct {
		role          string
		user, groupID any
	}{{"user", ids["bob"], nil}, {"reader", ids["carol"], nil}, {"reader", nil, group}} {
		body := map[string]any{"projectKey": upgradeProject, "role": g.role, "userId": g.user, "groupId": g.groupID}
		if err := s.send(ctx, http.MethodPost, "/role-assignments", body, nil); err != nil && !hasStatus(err, http.StatusConflict) {
			t.Fatalf("grant %s in Armature's %s: %v", g.role, upgradeProject, err)
		}
	}
}

// collectContent lists the sample and the links within and between the
// tools as their administrators see them, one line each, and names the
// objects everybody is asked about.
func collectContent(t *testing.T, ctx context.Context, admins map[string]*session) (map[string][]string, map[string]string) {
	t.Helper()
	content := map[string][]string{}
	refs := map[string]string{}
	add := func(kind, format string, args ...any) {
		content[kind] = append(content[kind], fmt.Sprintf(format, args...))
	}
	get := func(s *session, path string, out any) {
		t.Helper()
		if err := s.send(ctx, http.MethodGet, path, nil, out); err != nil {
			t.Fatal(err)
		}
	}
	ar, st, ex := admins["armature"], admins["stator"], admins["exciter"]

	var issues struct {
		Issues []struct {
			Key     string `json:"key"`
			Summary string `json:"summary"`
		} `json:"issues"`
	}
	get(ar, "/projects/"+upgradeProject+"/issues?limit=100", &issues)
	for _, i := range issues.Issues {
		add("Armature: the issues", "%s %s", i.Key, i.Summary)
		var links struct {
			Links []struct {
				Direction string `json:"direction"`
				Issue     struct {
					Key string `json:"key"`
				} `json:"issue"`
			} `json:"links"`
		}
		get(ar, "/issues/"+i.Key+"/links", &links)
		for _, l := range links.Links {
			add("Armature: links between issues", "%s %s %s", i.Key, l.Direction, l.Issue.Key)
		}
		var remote struct {
			RemoteLinks []struct {
				URL      string `json:"url"`
				Relation string `json:"relation"`
			} `json:"remoteLinks"`
		}
		get(ar, "/issues/"+i.Key+"/remote-links", &remote)
		for _, l := range remote.RemoteLinks {
			add("Armature: remote links on issues", "%s -> %s (%s)", i.Key, l.URL, l.Relation)
		}
	}
	if len(issues.Issues) == 0 {
		t.Fatalf("Armature's %s has no issues; sample-data imports them before this runs", upgradeProject)
	}
	keys := make([]string, 0, len(issues.Issues))
	for _, i := range issues.Issues {
		keys = append(keys, i.Key)
	}
	refs["issue"] = slices.Min(keys)

	var space struct {
		Space struct {
			HomePageID string `json:"homePageId"`
		} `json:"space"`
	}
	get(st, "/spaces/"+upgradeProject, &space)
	refs["home page"] = space.Space.HomePageID
	var outline struct {
		Pages []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"pages"`
	}
	get(st, "/spaces/"+upgradeProject+"/outline", &outline)
	for _, p := range outline.Pages {
		add("Stator: the pages", "%s %s", p.ID, p.Title)
		if p.Title == "Changelog" {
			refs["page"] = p.ID
		}
		var remote struct {
			RemoteLinks []struct {
				URL string `json:"url"`
			} `json:"remoteLinks"`
		}
		get(st, "/pages/"+p.ID+"/remote-links", &remote)
		for _, l := range remote.RemoteLinks {
			add("Stator: remote links on pages", "%s -> %s", p.ID, l.URL)
		}
	}
	if refs["page"] == "" {
		t.Fatalf("Stator's %s has no page Changelog; sample-data imports it before this runs", upgradeProject)
	}

	var reqs struct {
		Rows []struct {
			Key   string `json:"key"`
			Title string `json:"title"`
		} `json:"rows"`
	}
	get(ex, "/projects/"+upgradeProject+"/requirements?limit=100", &reqs)
	for _, r := range reqs.Rows {
		add("Exciter: the requirements", "%s %s", r.Key, r.Title)
		var traces struct {
			Links []struct {
				IssueKey string `json:"issueKey"`
				Relation string `json:"relation"`
			} `json:"links"`
		}
		get(ex, "/requirements/"+r.Key+"/armature-links", &traces)
		for _, l := range traces.Links {
			add("Exciter: traces to Armature issues", "%s -> %s (%s)", r.Key, l.IssueKey, l.Relation)
			if refs["requirement"] == "" || r.Key < refs["requirement"] {
				refs["requirement"] = r.Key
			}
		}
		var pages struct {
			Links []struct {
				PageID   string `json:"pageId"`
				Relation string `json:"relation"`
			} `json:"links"`
		}
		get(ex, "/requirements/"+r.Key+"/stator-links", &pages)
		for _, l := range pages.Links {
			add("Exciter: links to Stator pages", "%s -> %s (%s)", r.Key, l.PageID, l.Relation)
		}
		var links struct {
			Links []struct {
				Direction string `json:"direction"`
				Other     struct {
					Key string `json:"key"`
				} `json:"other"`
			} `json:"links"`
		}
		get(ex, "/requirements/"+r.Key+"/links", &links)
		for _, l := range links.Links {
			add("Exciter: links between requirements", "%s %s %s", r.Key, l.Direction, l.Other.Key)
		}
	}
	if refs["requirement"] == "" {
		t.Fatalf("no requirement of Exciter's %s is traced to Armature; sample-data traces them before this runs", upgradeProject)
	}
	for kind := range content {
		slices.Sort(content[kind])
	}
	return content, refs
}

// probe is one question to a tool about what the caller may do: the answer's
// status, and the fields at its path when keep is empty or those it names.
type probe struct {
	product, what, path string
	at, keep            []string
}

func accessProbes(refs map[string]string) []probe {
	k := upgradeProject
	return []probe{
		{"stator", "what they may do across Stator", "/access/me", []string{"can"}, nil},
		{"stator", "the space " + k, "/spaces/" + k, []string{"space", "can"}, nil},
		{"stator", "the space's home page", "/pages/" + refs["home page"], []string{"page", "can"}, nil},
		{"stator", "the page Changelog", "/pages/" + refs["page"], []string{"page", "can"}, nil},
		{"armature", "what they may do across Armature", "/access/me", nil, []string{"canAdministerOrg", "canCreateProject", "permissions", "projects"}},
		{"armature", "the project " + k, "/projects/" + k, nil, nil},
		{"armature", "the issue " + refs["issue"], "/issues/" + refs["issue"], nil, nil},
		{"exciter", "what they may do across Exciter", "/access/me", []string{"can"}, nil},
		{"exciter", "the project " + k, "/projects/" + k, []string{"project", "can"}, nil},
		{"exciter", "the requirement " + refs["requirement"], "/requirements/" + refs["requirement"], []string{"requirement", "can"}, nil},
	}
}

// readAccess asks every tool, as each person, what they may do.
func readAccess(t *testing.T, env map[string]string, browsers map[string]*http.Client, refs map[string]string) map[string]map[string]reading {
	t.Helper()
	out := map[string]map[string]reading{}
	for _, p := range upgradePeople {
		out[p.name] = map[string]reading{}
		for _, q := range accessProbes(refs) {
			status, raw := personRead(t, browsers[p.name], env[strings.ToUpper(q.product)+"_URL"], q.path)
			r := reading{Status: status}
			if status == http.StatusOK && (q.at != nil || q.keep != nil) {
				r.Body = answerPart(t, raw, q)
			}
			out[p.name][displayName[q.product]+": "+q.what] = r
		}
	}
	return out
}

func personRead(t *testing.T, b *http.Client, base, path string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/api/v1"+path, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := b.Do(req)
	if err != nil {
		t.Fatalf("GET %s at %s: %v", path, base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func answerPart(t *testing.T, raw []byte, q probe) json.RawMessage {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s answered GET %s with something that is not JSON: %.200s", displayName[q.product], q.path, raw)
	}
	for _, field := range q.at {
		m, _ := v.(map[string]any)
		v = m[field]
	}
	if q.keep != nil {
		m, _ := v.(map[string]any)
		kept := map[string]any{}
		for _, field := range q.keep {
			if f, ok := m[field]; ok {
				kept[field] = f
			}
		}
		v = kept
	}
	if v == nil {
		t.Fatalf("%s answered GET %s without %s: %.300s", displayName[q.product], q.path, strings.Join(q.at, "."), raw)
	}
	part, _ := json.Marshal(v)
	return part
}

// checkSeeded makes sure the seed gave each person the access it meant to,
// where the tool's answer says so plainly, so the record is worth comparing.
func checkSeeded(t *testing.T, access map[string]map[string]reading) {
	t.Helper()
	k := upgradeProject
	for _, c := range []struct {
		person, what, field string
		want                bool
	}{
		{"alice", "Stator: what they may do across Stator", "administer", true},
		{"alice", "Exciter: what they may do across Exciter", "administer", true},
		{"alice", "Armature: what they may do across Armature", "canAdministerOrg", true},
		{"bob", "Stator: the space " + k, "editPages", true},
		{"bob", "Exciter: the project " + k, "edit", true},
		{"carol", "Stator: the space " + k, "editPages", false},
		{"carol", "Exciter: the project " + k, "view", true},
		{"carol", "Exciter: the project " + k, "edit", false},
		{"dave", "Stator: the space " + k, "addComments", true},
		{"dave", "Stator: the space " + k, "editPages", false},
		{"dave", "Exciter: the project " + k, "comment", true},
		{"dave", "Exciter: the project " + k, "edit", false},
	} {
		r := access[c.person][c.what]
		var fields map[string]any
		_ = json.Unmarshal(r.Body, &fields)
		if got, ok := fields[c.field].(bool); r.Status != http.StatusOK || !ok || got != c.want {
			t.Errorf("the seed meant %s to have %s %v for %s, but the answer was %d %s", c.person, c.field, c.want, c.what, r.Status, r.Body)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

// linksResolve follows the links between the tools: Exciter looks up each
// traced issue and page through its partners, and Stator still serves the
// pages Armature's issues link to.
func linksResolve(t *testing.T, ctx context.Context, admins map[string]*session, env map[string]string, content map[string][]string) {
	t.Helper()
	target := func(line string) string {
		_, to, _ := strings.Cut(line, " -> ")
		to, _, _ = strings.Cut(to, " (")
		return to
	}
	for _, line := range content["Exciter: traces to Armature issues"] {
		key := target(line)
		var got struct {
			Issues []struct {
				Issue *struct {
					Key string `json:"key"`
				} `json:"issue"`
			} `json:"issues"`
			Status string `json:"status"`
		}
		if err := admins["exciter"].send(ctx, http.MethodGet, "/armature/issues?key="+url.QueryEscape(key), nil, &got); err != nil {
			t.Errorf("Exciter could not look up %s in Armature: %v", key, err)
			continue
		}
		if got.Status != "ok" || len(got.Issues) == 0 || got.Issues[0].Issue == nil {
			t.Errorf("the trace %s does not resolve: Exciter's lookup in Armature says %q with %d issues", line, got.Status, len(got.Issues))
		}
	}
	for _, line := range content["Exciter: links to Stator pages"] {
		id := target(line)
		var got struct {
			Pages []struct {
				Page *struct {
					ID string `json:"id"`
				} `json:"page"`
			} `json:"pages"`
			Status string `json:"status"`
		}
		if err := admins["exciter"].send(ctx, http.MethodGet, "/stator/pages?id="+url.QueryEscape(id), nil, &got); err != nil {
			t.Errorf("Exciter could not look up the page %s in Stator: %v", id, err)
			continue
		}
		if got.Status != "ok" || len(got.Pages) == 0 || got.Pages[0].Page == nil {
			t.Errorf("the link %s does not resolve: Exciter's lookup in Stator says %q with %d pages", line, got.Status, len(got.Pages))
		}
	}
	stator := strings.TrimRight(env["STATOR_URL"], "/")
	for _, line := range content["Armature: remote links on issues"] {
		to := target(line)
		m := pageID.FindStringSubmatch(to)
		if !strings.HasPrefix(to, stator+"/") || m == nil {
			continue
		}
		if err := admins["stator"].send(ctx, http.MethodGet, "/pages/"+m[1], nil, nil); err != nil {
			t.Errorf("the remote link %s does not resolve: %v", line, err)
		}
	}
}
