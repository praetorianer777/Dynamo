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
)

// fakeKeycloak answers the admin routes realmClients calls, for the realm
// dynamo, to the master realm's administrator only.
type fakeKeycloak struct {
	mu      sync.Mutex
	clients map[string]map[string]any
	secrets map[string]string
	// Calls outside the clients, such as to the realm's people.
	elsewhere int
	puts      int
}

func newFakeKeycloak() *fakeKeycloak {
	k := &fakeKeycloak{clients: map[string]map[string]any{}, secrets: map[string]string{}}
	for _, p := range []string{"stator", "armature", "exciter"} {
		k.clients[p+"-id"] = map[string]any{
			"id": p + "-id", "clientId": p + "-client", "protocol": "openid-connect",
			"redirectUris": []any{"https://old.example.com/" + p + "/*"}, "webOrigins": []any{"https://old.example.com/" + p},
			"attributes": map[string]any{"post.logout.redirect.uris": "+"},
		}
		k.secrets[p+"-id"] = "imported"
	}
	return k
}

func (k *fakeKeycloak) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k.mu.Lock()
	defer k.mu.Unlock()
	answer := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	if r.URL.Path == "/realms/master/protocol/openid-connect/token" {
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "admin-cli" || r.Form.Get("username") != "admin" || r.Form.Get("password") != "kc password" {
			answer(401, map[string]string{"error": "invalid_grant"})
			return
		}
		answer(200, map[string]string{"access_token": "admin-token"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer admin-token" {
		answer(401, map[string]string{"error": "HTTP 401 Unauthorized"})
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/admin/realms/dynamo/clients")
	if !ok {
		k.elsewhere++
		answer(404, map[string]string{"error": "Realm not found."})
		return
	}
	id, sub, _ := strings.Cut(strings.Trim(rest, "/"), "/")
	switch {
	case id == "" && r.Method == http.MethodGet:
		found := []map[string]any{}
		for _, c := range k.clients {
			if c["clientId"] == r.URL.Query().Get("clientId") {
				found = append(found, c)
			}
		}
		answer(200, found)
	case k.clients[id] == nil:
		answer(404, map[string]string{"error": "Could not find client"})
	case sub == "client-secret" && r.Method == http.MethodGet:
		answer(200, map[string]string{"type": "secret", "value": k.secrets[id]})
	case sub == "" && r.Method == http.MethodPut:
		var rep map[string]any
		_ = json.NewDecoder(r.Body).Decode(&rep)
		if s, ok := rep["secret"].(string); ok {
			k.secrets[id] = s
			delete(rep, "secret")
		}
		k.clients[id] = rep
		k.puts++
		w.WriteHeader(204)
	default:
		answer(405, map[string]string{"error": "no such route"})
	}
}

func startKeycloak(t *testing.T, cfg *config) *fakeKeycloak {
	t.Helper()
	k := newFakeKeycloak()
	srv := httptest.NewServer(k)
	t.Cleanup(srv.Close)
	cfg.keycloak = &keycloakAdmin{url: srv.URL, username: "admin", password: "kc password"}
	return k
}

func TestTheRealmsClientsFollowTheAddressesAndSecrets(t *testing.T) {
	cfg, _ := startFakes(t)
	k := startKeycloak(t, &cfg)
	ctx := context.Background()
	if _, err := run(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"stator", "armature", "exciter"} {
		c, url := k.clients[p+"-id"], cfg.toolByProduct(p).url
		if got := toStrings(c["redirectUris"]); len(got) != 1 || got[0] != url+"/*" {
			t.Errorf("%s's client returns to %v, want only %s/*", p, got, url)
		}
		if got := toStrings(c["webOrigins"]); len(got) != 1 || got[0] != url {
			t.Errorf("%s's client allows the origins %v, want only %s", p, got, url)
		}
		if k.secrets[p+"-id"] != cfg.toolByProduct(p).clientSecret {
			t.Errorf("%s's client kept the secret %q", p, k.secrets[p+"-id"])
		}
		if c["attributes"] == nil || c["protocol"] != "openid-connect" {
			t.Errorf("%s's client lost what it held besides: %v", p, c)
		}
	}

	puts := k.puts
	if again, err := run(ctx, cfg); err != nil || len(again) != 0 || k.puts != puts {
		t.Fatalf("the run after changed %v (%d updates), %v", again, k.puts-puts, err)
	}

	cfg.exciter.url = "https://exciter.example.org"
	cfg.stator.clientSecret = "rotated"
	var changes []string
	note := func(format string, args ...any) { changes = append(changes, fmt.Sprintf(format, args...)) }
	if err := realmClients(ctx, cfg, note); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || k.secrets["stator-id"] != "rotated" || toStrings(k.clients["exciter-id"]["redirectUris"])[0] != "https://exciter.example.org/*" {
		t.Fatalf("a changed address and secret did not reach the realm: %v", changes)
	}
	if k.elsewhere != 0 {
		t.Errorf("dynamo-connect called Keycloak %d times outside the realm's clients", k.elsewhere)
	}
}

func TestAWrongKeycloakAdministratorIsNamed(t *testing.T) {
	cfg, _ := startFakes(t)
	startKeycloak(t, &cfg)
	cfg.keycloak.password = "wrong"
	_, err := run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "check DYNAMO_KEYCLOAK_ADMIN_USERNAME") {
		t.Fatalf("want the administrator setting named, got %v", err)
	}
}

func TestTheRealmIsTheIssuers(t *testing.T) {
	if got, err := realmOf("https://sso.example.com/realms/dynamo"); err != nil || got != "dynamo" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"https://sso.example.com", "https://sso.example.com/realms/", "https://accounts.example.com/o/oauth2"} {
		if _, err := realmOf(bad); err == nil {
			t.Errorf("%q was taken for a realm", bad)
		}
	}
}
