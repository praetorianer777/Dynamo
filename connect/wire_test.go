package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTool answers the admin routes the job calls the way the tools do,
// refusing every call but meta and sign-in without the session cookie.
type fakeTool struct {
	product, version string
	email, password  string

	mu          sync.Mutex
	signedUp    bool
	provider    *oidcProvider
	users       map[string]bool
	connections map[string]*connection
	secrets     map[string]string
	webhooks    []webhook
	topics      []string
}

func newFakeTool(product, version string) *fakeTool {
	return &fakeTool{
		product: product, version: version,
		email: "admin@" + product + ".test", password: product + " password",
		users: map[string]bool{}, connections: map[string]*connection{}, secrets: map[string]string{},
		topics: []string{"issue.updated", product + ".changed"},
	}
}

func (f *fakeTool) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	answer := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	route := r.Method + " " + path
	switch route {
	case "GET /meta":
		answer(200, map[string]any{"product": f.product, "build": map[string]string{"version": f.version}})
		return
	case "GET /auth/signup":
		answer(200, map[string]bool{"open": !f.signedUp, "first": !f.signedUp})
		return
	case "POST /auth/signup", "POST /auth/login":
		if route == "POST /auth/signup" {
			if f.signedUp {
				answer(409, map[string]string{"error": "set up already"})
				return
			}
			f.signedUp = true
		}
		if body["email"] != f.email || body["password"] != f.password {
			answer(401, map[string]string{"error": "wrong password"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: f.product + "_session", Value: "ok", Secure: true, HttpOnly: true})
		answer(200, map[string]string{})
		return
	}
	if c, err := r.Cookie(f.product + "_session"); err != nil || c.Value != "ok" {
		answer(401, map[string]string{"error": "sign in first"})
		return
	}
	switch {
	case route == "GET /oidc-provider":
		if f.provider == nil {
			answer(404, map[string]string{"error": "none"})
			return
		}
		answer(200, map[string]any{"provider": f.provider})
	case route == "PUT /oidc-provider":
		f.provider = &oidcProvider{Issuer: body["issuer"].(string), ClientID: body["clientId"].(string), GroupsClaim: body["groupsClaim"].(string), Enabled: true}
		answer(200, map[string]any{"provider": f.provider})
	case route == "POST /users":
		email := body["email"].(string)
		if f.users[email] {
			answer(409, map[string]string{"error": "exists"})
			return
		}
		f.users[email] = true
		answer(201, map[string]string{})
	case strings.HasSuffix(path, "/connection"):
		partner := strings.Trim(strings.TrimSuffix(path, "/connection"), "/")
		c := f.connections[partner]
		if r.Method == http.MethodGet {
			if c == nil {
				answer(404, map[string]string{"error": "not connected"})
				return
			}
			answer(200, map[string]any{"connection": c})
			return
		}
		if c == nil || c.BaseURL != body["baseUrl"] {
			c = &connection{BaseURL: body["baseUrl"].(string), WebhookTopics: f.topics}
			f.connections[partner] = c
		}
		if s, ok := body["webhookSecret"].(string); ok {
			f.secrets[partner] = s
			c.WebhookSecretSet = true
		}
		answer(200, map[string]any{"connection": c})
	case route == "GET /webhooks":
		answer(200, map[string]any{"webhooks": f.webhooks})
	case route == "POST /webhooks":
		h := webhook{ID: fmt.Sprint(len(f.webhooks) + 1), Name: body["name"].(string), URL: body["url"].(string), Topics: toStrings(body["topics"])}
		f.webhooks = append(f.webhooks, h)
		answer(201, map[string]any{"webhook": webhook{ID: h.ID, Secret: "secret-" + h.ID}})
	default:
		answer(404, map[string]string{"error": "no route " + route})
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func testConfig(stator, armature, exciter *httptest.Server, fs map[string]*fakeTool) config {
	at := func(product string, srv *httptest.Server) tool {
		f := fs[product]
		return tool{product: product, api: srv.URL, url: "http://localhost/" + product, version: f.version, email: f.email, password: f.password,
			clientID: product + "-client", clientSecret: "s"}
	}
	return config{
		stator: at("stator", stator), armature: at("armature", armature), exciter: at("exciter", exciter),
		issuer: "http://localhost:8180/realms/dynamo", groupsClaim: "groups",
		members: []member{{"alice@suite.test", "admin"}, {"bob@suite.test", "member"}},
		waitFor: time.Second,
	}
}

func startFakes(t *testing.T) (config, map[string]*fakeTool) {
	t.Helper()
	fs := map[string]*fakeTool{
		"stator": newFakeTool("stator", "1.0.0"), "armature": newFakeTool("armature", "2.0.0"), "exciter": newFakeTool("exciter", "3.0.0"),
	}
	s, a, e := httptest.NewServer(fs["stator"]), httptest.NewServer(fs["armature"]), httptest.NewServer(fs["exciter"])
	t.Cleanup(func() { s.Close(); a.Close(); e.Close() })
	return testConfig(s, a, e, fs), fs
}

func TestConnectsAllThreeAndASecondRunChangesNothing(t *testing.T) {
	cfg, fs := startFakes(t)
	ctx := context.Background()

	changes, err := run(ctx, cfg)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(changes) == 0 {
		t.Fatal("the first run changed nothing")
	}
	if !fs["armature"].signedUp || !fs["armature"].users["bob@suite.test"] {
		t.Fatalf("Armature was not set up: %+v", fs["armature"])
	}
	for product, f := range fs {
		if f.provider == nil || f.provider.Issuer != cfg.issuer || f.provider.ClientID != product+"-client" {
			t.Errorf("%s does not sign in through the realm with its own client: %+v", product, f.provider)
		}
	}
	for receiver, senders := range map[string][]string{"stator": {"armature", "exciter"}, "exciter": {"armature", "stator"}} {
		for _, sender := range senders {
			c := fs[receiver].connections[sender]
			if c == nil || c.BaseURL != "http://localhost/"+sender || !c.WebhookSecretSet {
				t.Errorf("%s is not connected to %s: %+v", receiver, sender, c)
			}
			hooks := fs[sender].webhooks
			found := false
			for _, h := range hooks {
				if h.Name == displayName[receiver] && h.URL == cfg.toolByProduct(receiver).api+"/api/v1"+webhookPath[sender] {
					found = true
				}
			}
			if !found {
				t.Errorf("%s has no webhook for %s: %+v", sender, receiver, hooks)
			}
		}
	}

	again, err := run(ctx, cfg)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("the second run changed %v", again)
	}
}

func TestRefusesAToolAtAnotherVersionThanThePin(t *testing.T) {
	cfg, _ := startFakes(t)
	cfg.exciter.version = "9.9.9"
	_, err := run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "versions.yaml pins 9.9.9") {
		t.Fatalf("want the pin named in the error, got %v", err)
	}
}

func TestMembersAreEmailAndRole(t *testing.T) {
	got, err := parseMembers("alice@suite.test=admin, bob@suite.test=member,")
	if err != nil || len(got) != 2 || got[1] != (member{"bob@suite.test", "member"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{"alice@suite.test", "alice=admin", "alice@suite.test=owner"} {
		if _, err := parseMembers(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestAnotherRealmReachesARunningInstallation(t *testing.T) {
	cfg, fs := startFakes(t)
	ctx := context.Background()
	if _, err := run(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.issuer = "https://sso.example.com/realms/acme"
	cfg.stator.clientID = "wiki"
	changes, err := run(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 || fs["stator"].provider.Issuer != cfg.issuer || fs["stator"].provider.ClientID != "wiki" {
		t.Fatalf("want the three sign-ins moved to the new realm, got %v and %+v", changes, fs["stator"].provider)
	}
}

func TestLoadNamesEveryMissingSetting(t *testing.T) {
	_, err := load(func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "DYNAMO_STATOR_API") || !strings.Contains(err.Error(), "DYNAMO_OIDC_ISSUER") {
		t.Fatalf("got %v", err)
	}
}

func (c config) toolByProduct(product string) tool {
	switch product {
	case "stator":
		return c.stator
	case "armature":
		return c.armature
	}
	return c.exciter
}
