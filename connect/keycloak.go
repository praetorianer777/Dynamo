package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// keycloakAdmin is Keycloak's administrator as dynamo-connect reaches it, set
// only where the realm outlives its import, as the Compose server's does.
type keycloakAdmin struct {
	url, username, password string
}

type keycloakClient struct {
	http  *http.Client
	base  string
	realm string
	token string
}

// realmClients keeps each tool's client in the realm at the tool's address
// and secret, as realm.server.json imports them, leaving people alone.
func realmClients(ctx context.Context, cfg config, note func(string, ...any)) error {
	realm, err := realmOf(cfg.issuer)
	if err != nil {
		return err
	}
	kc := &keycloakClient{http: &http.Client{Timeout: callTimeout}, base: strings.TrimRight(cfg.keycloak.url, "/"), realm: realm}
	if err := kc.signIn(ctx, cfg.keycloak.username, cfg.keycloak.password); err != nil {
		return err
	}
	for _, t := range []tool{cfg.stator, cfg.armature, cfg.exciter} {
		if err := kc.keepClient(ctx, t, note); err != nil {
			return err
		}
	}
	return nil
}

func realmOf(issuer string) (string, error) {
	_, realm, ok := strings.Cut(issuer, "/realms/")
	realm = strings.Trim(realm, "/")
	if !ok || realm == "" || strings.Contains(realm, "/") {
		return "", fmt.Errorf("DYNAMO_OIDC_ISSUER %q names no Keycloak realm; with DYNAMO_KEYCLOAK_ADMIN_URL set it is <keycloak>/realms/<realm>", issuer)
	}
	return realm, nil
}

func (kc *keycloakClient) signIn(ctx context.Context, username, password string) error {
	form := url.Values{"grant_type": {"password"}, "client_id": {"admin-cli"}, "username": {username}, "password": {password}}
	var answer struct {
		AccessToken string `json:"access_token"`
	}
	if err := kc.do(ctx, http.MethodPost, "/realms/master/protocol/openid-connect/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), &answer); err != nil {
		return fmt.Errorf("sign in to Keycloak at %s as %s; check DYNAMO_KEYCLOAK_ADMIN_USERNAME and its password: %w", kc.base, username, err)
	}
	kc.token = answer.AccessToken
	return nil
}

func (kc *keycloakClient) keepClient(ctx context.Context, t tool, note func(string, ...any)) error {
	clients := "/admin/realms/" + url.PathEscape(kc.realm) + "/clients"
	var found []map[string]any
	if err := kc.send(ctx, http.MethodGet, clients+"?clientId="+url.QueryEscape(t.clientID), nil, &found); err != nil {
		return err
	}
	if len(found) == 0 {
		return fmt.Errorf("the realm %s has no client %s for %s; add it as docs/keycloak.md says, or set the client id", kc.realm, t.clientID, displayName[t.product])
	}
	rep := found[0]
	id, _ := rep["id"].(string)
	var secret struct {
		Value string `json:"value"`
	}
	if err := kc.send(ctx, http.MethodGet, clients+"/"+id+"/client-secret", nil, &secret); err != nil {
		return err
	}

	address := strings.TrimRight(t.url, "/")
	redirects, origins := []string{address + "/*"}, []string{address}
	var changed []string
	if !sameSet(toStrings(rep["redirectUris"]), redirects) || !sameSet(toStrings(rep["webOrigins"]), origins) {
		rep["redirectUris"], rep["webOrigins"] = redirects, origins
		changed = append(changed, "returns to "+address)
	}
	if secret.Value != t.clientSecret {
		rep["secret"] = t.clientSecret
		changed = append(changed, "has its new secret")
	}
	if len(changed) == 0 {
		return nil
	}
	if err := kc.send(ctx, http.MethodPut, clients+"/"+id, rep, nil); err != nil {
		return fmt.Errorf("update the realm's client %s: %w", t.clientID, err)
	}
	note("the realm's client %s %s", t.clientID, strings.Join(changed, " and "))
	return nil
}

func (kc *keycloakClient) send(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	contentType := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r, contentType = bytes.NewReader(raw), "application/json"
	}
	return kc.do(ctx, method, path, contentType, r, out)
}

func (kc *keycloakClient) do(ctx context.Context, method, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, kc.base+path, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if kc.token != "" {
		req.Header.Set("Authorization", "Bearer "+kc.token)
	}
	resp, err := kc.http.Do(req)
	if err != nil {
		return fmt.Errorf("Keycloak did not answer %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= http.StatusBadRequest {
		return &refusal{product: "keycloak", method: method, path: path, status: resp.StatusCode, body: strings.TrimSpace(string(raw))}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("Keycloak answered %s %s with something that is not the expected JSON: %w", method, path, err)
		}
	}
	return nil
}

func toStrings(v any) []string {
	out := []string{}
	xs, _ := v.([]any)
	for _, x := range xs {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
