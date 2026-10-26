package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
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

	mu       sync.Mutex
	signedUp bool
	provider *oidcProvider
	// The client secret its sign-in holds, which it never answers.
	clientSecret string
	users        map[string]bool
	connections  map[string]*connection
	secrets      map[string]string
	webhooks     []webhook
	topics       []string
	// What the tool offers as a provider of the token exchange, and the
	// partner apps it holds; appIDs and appSecrets are what it holds as a caller.
	exchange   bool
	apps       []partnerApp
	made       int
	appIDs     map[string]string
	appSecrets map[string]string
}

func newFakeTool(product, version string) *fakeTool {
	return &fakeTool{
		product: product, version: version,
		email: "admin@" + product + ".test", password: product + " password",
		users: map[string]bool{}, connections: map[string]*connection{}, secrets: map[string]string{},
		topics:   []string{"issue.updated", product + ".changed"},
		exchange: true, appIDs: map[string]string{}, appSecrets: map[string]string{},
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
		var caps []string
		if f.exchange {
			caps = append(caps, exchangeCapability)
		}
		answer(200, map[string]any{"product": f.product, "build": map[string]string{"version": f.version}, "capabilities": caps})
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
		f.clientSecret, _ = body["clientSecret"].(string)
		f.provider = &oidcProvider{Issuer: body["issuer"].(string), ClientID: body["clientId"].(string), GroupsClaim: body["groupsClaim"].(string), Enabled: true, HasSecret: f.clientSecret != ""}
		answer(200, map[string]any{"provider": f.provider})
	case route == "POST /users":
		email := body["email"].(string)
		if f.users[email] {
			answer(409, map[string]string{"error": "exists"})
			return
		}
		f.users[email] = true
		answer(201, map[string]string{})
	case strings.HasSuffix(path, "/connection/app") && r.Method == http.MethodPut:
		partner := strings.Trim(strings.TrimSuffix(path, "/connection/app"), "/")
		if f.product != "stator" || f.connections[partner] == nil {
			answer(404, map[string]string{"error": "no such route or no connection"})
			return
		}
		f.appIDs[partner], f.appSecrets[partner] = body["clientId"].(string), body["clientSecret"].(string)
		answer(200, map[string]any{"connection": f.connectionView(partner)})
	case strings.HasSuffix(path, "/connection"):
		partner := strings.Trim(strings.TrimSuffix(path, "/connection"), "/")
		c := f.connections[partner]
		if r.Method == http.MethodGet {
			if c == nil {
				answer(404, map[string]string{"error": "not connected"})
				return
			}
			answer(200, map[string]any{"connection": f.connectionView(partner)})
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
		if f.product == "exciter" {
			if id, ok := body["clientId"].(string); ok {
				f.appIDs[partner] = id
			}
			if s, ok := body["clientSecret"].(string); ok {
				f.appSecrets[partner] = s
			}
		}
		answer(200, map[string]any{"connection": f.connectionView(partner)})
	case route == "GET /oauth/clients":
		listed := []partnerApp{}
		for _, a := range f.apps {
			a.Secret = ""
			listed = append(listed, a)
		}
		answer(200, map[string]any{"clients": listed})
	case route == "POST /oauth/clients":
		app := partnerApp{Name: body["name"].(string), KeycloakClientID: body["keycloakClientId"].(string), Scopes: toStrings(body["scopes"])}
		if u, ok := body["clientUri"].(string); ok {
			app.ClientURI = u
		}
		if f.product == "armature" {
			if app.RedirectURIs = toStrings(body["redirectUris"]); len(app.RedirectURIs) == 0 {
				answer(422, map[string]string{"error": "redirectUris"})
				return
			}
			app.Status = "active"
		}
		f.made++
		app.ID = fmt.Sprintf("%s-app-%d", f.product, f.made)
		app.Secret = "secret-of-" + app.ID
		f.apps = append(f.apps, app)
		answer(201, map[string]any{"client": app})
	case strings.HasPrefix(path, "/oauth/clients/"):
		id, action, _ := strings.Cut(strings.TrimPrefix(path, "/oauth/clients/"), "/")
		at := -1
		for i, a := range f.apps {
			if a.ID == id {
				at = i
			}
		}
		switch {
		case at < 0 || (action == "rotate-secret" && f.product == "stator") || (action == "approve" && f.product != "armature"):
			answer(404, map[string]string{"error": "no such app or route"})
		case r.Method == http.MethodDelete:
			f.apps = slices.Delete(f.apps, at, at+1)
			w.WriteHeader(204)
		case r.Method == http.MethodPatch:
			a := &f.apps[at]
			for k, v := range body {
				switch k {
				case "name":
					a.Name = v.(string)
				case "keycloakClientId":
					a.KeycloakClientID = v.(string)
				case "clientUri":
					a.ClientURI = v.(string)
				case "scopes":
					a.Scopes = toStrings(v)
				case "redirectUris":
					a.RedirectURIs = toStrings(v)
				}
			}
			answer(200, map[string]any{"client": *a})
		case action == "rotate-secret":
			f.made++
			f.apps[at].Secret = fmt.Sprintf("secret-of-%s-%d", id, f.made)
			answer(200, map[string]any{"client": f.apps[at]})
		case action == "approve":
			f.apps[at].Status = "active"
			answer(200, map[string]any{"client": f.apps[at]})
		}
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

// connectionView answers a connection as the tool does: Exciter names its
// app on the connection, Stator under app, its Armature app by who stored it.
func (f *fakeTool) connectionView(partner string) map[string]any {
	c := f.connections[partner]
	view := map[string]any{"baseUrl": c.BaseURL, "webhookSecretSet": c.WebhookSecretSet, "webhookTopics": c.WebhookTopics}
	id, set := f.appIDs[partner], f.appSecrets[partner] != ""
	switch {
	case f.product == "exciter":
		view["clientId"], view["clientSecretSet"] = id, set
	case f.product == "stator" && partner == "armature":
		app := map[string]any{"clientId": nil, "registeredBy": nil, "status": "none"}
		if id != "" && set {
			app = map[string]any{"clientId": id, "registeredBy": "admin", "status": "active"}
		}
		view["app"] = app
	case f.product == "stator":
		var clientID any
		if id != "" {
			clientID = id
		}
		view["app"] = map[string]any{"clientId": clientID, "secretSet": set}
	}
	return view
}

// appOf answers the provider's app for the caller.
func (f *fakeTool) appOf(caller string) *partnerApp {
	for i := range f.apps {
		if f.apps[i].Name == displayName[caller] {
			return &f.apps[i]
		}
	}
	return nil
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
	cfg := testConfig(s, a, e, fs)
	cfg.stateDir = t.TempDir()
	return cfg, fs
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

	checkExchange(t, cfg, fs)

	again, err := run(ctx, cfg)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("the second run changed %v", again)
	}
}

// exchangePairs are the callers that connect a person at sign-in and the
// providers they exchange the person's token at.
var exchangePairs = [][2]string{{"stator", "armature"}, {"stator", "exciter"}, {"exciter", "armature"}, {"exciter", "stator"}}

// checkExchange holds each caller to its app at each provider: registered with
// the caller's Keycloak client and address, its live secret stored in the caller.
func checkExchange(t *testing.T, cfg config, fs map[string]*fakeTool) {
	t.Helper()
	for _, p := range exchangePairs {
		caller, provider := p[0], p[1]
		ct := cfg.toolByProduct(caller)
		app := fs[provider].appOf(caller)
		if app == nil {
			t.Errorf("%s has no partner app for %s: %+v", provider, caller, fs[provider].apps)
			continue
		}
		if app.KeycloakClientID != ct.clientID || !sameSet(app.Scopes, exchangeScopes[provider]) {
			t.Errorf("%s's app for %s has the client %q and the scopes %v", provider, caller, app.KeycloakClientID, app.Scopes)
		}
		if provider != "stator" && app.ClientURI != ct.url {
			t.Errorf("%s's app for %s names the address %q, want %q", provider, caller, app.ClientURI, ct.url)
		}
		if provider == "armature" && !slices.Equal(app.RedirectURIs, []string{ct.url + "/api/v1/armature/oauth/callback"}) {
			t.Errorf("Armature's app for %s returns to %v", caller, app.RedirectURIs)
		}
		if fs[caller].appIDs[provider] != app.ID || fs[caller].appSecrets[provider] != app.Secret {
			t.Errorf("%s holds the app %q with the secret %q for %s, want %q with its live secret %q",
				caller, fs[caller].appIDs[provider], fs[caller].appSecrets[provider], provider, app.ID, app.Secret)
		}
	}
}

func TestAnAppWhoseSecretWasLostGetsANewOne(t *testing.T) {
	cfg, fs := startFakes(t)
	ctx := context.Background()
	if _, err := run(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	armatureApp := fs["armature"].appOf("exciter").ID
	statorApp := fs["stator"].appOf("exciter").ID
	delete(fs["exciter"].appSecrets, "armature")
	delete(fs["exciter"].appSecrets, "stator")
	fs["exciter"].appOf("stator").KeycloakClientID = "somebody-else"

	changes, err := run(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Errorf("want Exciter's two apps stored again and Stator's app for Stator corrected, got %v", changes)
	}
	checkExchange(t, cfg, fs)
	if got := fs["armature"].appOf("exciter").ID; got != armatureApp {
		t.Errorf("Armature's app for Exciter was made anew (%s, was %s) instead of given a new secret", got, armatureApp)
	}
	if got := fs["stator"].appOf("exciter").ID; got == statorApp || len(fs["stator"].apps) != 1 {
		t.Errorf("Stator, which issues no new secret, kept the app %s beside %v instead of one new app", statorApp, fs["stator"].apps)
	}
	if again, err := run(ctx, cfg); err != nil || len(again) != 0 {
		t.Fatalf("the run after changed %v, %v", again, err)
	}
}

func TestAPendingAppOfTheSameNameIsAllowed(t *testing.T) {
	cfg, fs := startFakes(t)
	fs["armature"].apps = []partnerApp{{ID: "self", Name: "Stator", Status: "pending", Secret: "registered-by-stator"}}
	if _, err := run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	checkExchange(t, cfg, fs)
	if app := fs["armature"].appOf("stator"); app.ID != "self" || app.Status != "active" {
		t.Errorf("want the app Stator registered itself allowed and kept, got %+v", app)
	}
}

func TestAProviderWithoutTheExchangeGetsNoApp(t *testing.T) {
	cfg, fs := startFakes(t)
	fs["stator"].exchange = false
	if _, err := run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(fs["stator"].apps) != 0 || fs["exciter"].appIDs["stator"] != "" {
		t.Errorf("Stator does not offer the exchange, yet holds the apps %v", fs["stator"].apps)
	}
	if fs["armature"].appOf("exciter") == nil {
		t.Error("Armature, which offers the exchange, has no app for Exciter")
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
	if len(changes) != 5 || fs["stator"].provider.Issuer != cfg.issuer || fs["stator"].provider.ClientID != "wiki" {
		t.Fatalf("want the three sign-ins moved to the new realm and Stator's two partner apps to its new client, got %v and %+v", changes, fs["stator"].provider)
	}
	checkExchange(t, cfg, fs)
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

func TestAChangedClientSecretReachesTheTool(t *testing.T) {
	cfg, fs := startFakes(t)
	ctx := context.Background()
	if _, err := run(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.armature.clientSecret = "rotated"
	changes, err := run(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || fs["armature"].clientSecret != "rotated" {
		t.Fatalf("want Armature alone given the new secret, got %v and Armature holding %q", changes, fs["armature"].clientSecret)
	}
	if again, err := run(ctx, cfg); err != nil || len(again) != 0 {
		t.Fatalf("the run after changed %v, %v", again, err)
	}
}

func TestASecretSentWithoutARecordIsSentAgain(t *testing.T) {
	cfg, fs := startFakes(t)
	ctx := context.Background()
	if _, err := run(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	fs["stator"].clientSecret = "set by hand"
	cfg.stateDir = t.TempDir()
	changes, err := run(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 || fs["stator"].clientSecret != cfg.stator.clientSecret {
		t.Fatalf("with nothing remembered, want each tool's secret sent once more, got %v and Stator holding %q", changes, fs["stator"].clientSecret)
	}
}

func TestAStateThatCannotBeKeptStopsTheRun(t *testing.T) {
	cfg, _ := startFakes(t)
	cfg.stateDir = filepath.Join(t.TempDir(), "missing")
	_, err := run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "give it a writable volume there") {
		t.Fatalf("want a sentence naming the volume, got %v", err)
	}
}
