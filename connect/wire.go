package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

// webhookPath is where each tool receives a partner's webhooks, under /api/v1.
var webhookPath = map[string]string{
	"armature": "/armature/webhook",
	"stator":   "/stator/webhook",
	"exciter":  "/exciter/webhook",
}

var displayName = map[string]string{"stator": "Stator", "armature": "Armature", "exciter": "Exciter"}

// run does every step in order and answers what it changed.
func run(ctx context.Context, cfg config) ([]string, error) {
	var changes []string
	note := func(format string, args ...any) { changes = append(changes, fmt.Sprintf(format, args...)) }

	st, err := loadState(cfg.stateDir)
	if err != nil {
		return changes, err
	}

	offers := map[string]bool{}
	for _, t := range []tool{cfg.stator, cfg.armature, cfg.exciter} {
		m, err := awaitMeta(ctx, t, cfg.waitFor)
		if err != nil {
			return changes, err
		}
		offers[t.product] = slices.Contains(m.Capabilities, exchangeCapability)
	}

	armature, err := armatureOwner(ctx, cfg.armature, note)
	if err != nil {
		return changes, err
	}
	if err := armatureMembers(ctx, armature, cfg.members, note); err != nil {
		return changes, err
	}

	stator := newSession(cfg.stator.product, cfg.stator.api)
	if err := stator.signIn(ctx, cfg.stator.email, cfg.stator.password); err != nil {
		return changes, err
	}
	exciter := newSession(cfg.exciter.product, cfg.exciter.api)
	if err := exciter.signIn(ctx, cfg.exciter.email, cfg.exciter.password); err != nil {
		return changes, err
	}
	for _, t := range []struct {
		s *session
		t tool
	}{{stator, cfg.stator}, {armature, cfg.armature}, {exciter, cfg.exciter}} {
		if err := ssoSignIn(ctx, t.s, t.t, cfg, st, note); err != nil {
			return changes, err
		}
	}

	// Each receiver connects to its sender, then the sender gets the receiver's
	// webhook, whose secret goes back to the receiver. The same pairs exchange
	// a person's sign-in, the receiver as the sender's partner app.
	pairs := []struct {
		receiver     *session
		receiverTool tool
		sender       *session
		senderTool   tool
	}{
		{stator, cfg.stator, armature, cfg.armature},
		{exciter, cfg.exciter, armature, cfg.armature},
		{stator, cfg.stator, exciter, cfg.exciter},
		{exciter, cfg.exciter, stator, cfg.stator},
	}
	for _, p := range pairs {
		if err := connectPair(ctx, p.receiver, p.receiverTool, p.sender, p.senderTool, note); err != nil {
			return changes, err
		}
		if !offers[p.senderTool.product] {
			slog.Info("no token exchange offered, so people connect with a token", "caller", p.receiverTool.product, "partner", p.senderTool.product)
			continue
		}
		if err := exchangeApp(ctx, p.receiver, p.receiverTool, p.sender, p.senderTool, note); err != nil {
			return changes, err
		}
	}
	return changes, nil
}

type meta struct {
	Product string `json:"product"`
	Build   struct {
		Version string `json:"version"`
	} `json:"build"`
	Capabilities []string `json:"capabilities"`
}

// awaitMeta waits until the tool answers as itself, at the pinned version.
func awaitMeta(ctx context.Context, t tool, waitFor time.Duration) (meta, error) {
	s := newSession(t.product, t.api)
	deadline := time.Now().Add(waitFor)
	for {
		var m meta
		err := s.send(ctx, http.MethodGet, "/meta", nil, &m)
		if err == nil {
			if !strings.EqualFold(m.Product, t.product) {
				return m, fmt.Errorf("%s answers as %q; DYNAMO_%s_API names another tool", t.api, m.Product, strings.ToUpper(t.product))
			}
			if t.version != "" && m.Build.Version != t.version {
				return m, fmt.Errorf("%s runs %s, but versions.yaml pins %s; pull the pinned image or change the pin", displayName[t.product], m.Build.Version, t.version)
			}
			slog.Info("answering", "tool", t.product, "version", m.Build.Version)
			return m, nil
		}
		if time.Now().After(deadline) {
			return meta{}, fmt.Errorf("%s did not answer GET /api/v1/meta within %s: %w", displayName[t.product], waitFor, err)
		}
		select {
		case <-ctx.Done():
			return meta{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// armatureOwner signs in as Armature's owner, making the owner first when
// the installation has nobody yet: whoever signs up first owns it.
func armatureOwner(ctx context.Context, t tool, note func(string, ...any)) (*session, error) {
	s := newSession(t.product, t.api)
	var signup struct {
		Open  bool `json:"open"`
		First bool `json:"first"`
	}
	if err := s.send(ctx, http.MethodGet, "/auth/signup", nil, &signup); err != nil {
		return nil, err
	}
	if signup.First {
		body := map[string]string{"email": t.email, "password": t.password, "name": "Administrator"}
		if err := s.send(ctx, http.MethodPost, "/auth/signup", body, nil); err != nil {
			return nil, fmt.Errorf("set Armature up with its first account: %w", err)
		}
		note("Armature was set up with %s as its owner", t.email)
		return s, nil
	}
	return s, s.signIn(ctx, t.email, t.password)
}

type oidcProvider struct {
	Issuer       string `json:"issuer"`
	ClientID     string `json:"clientId"`
	Enabled      bool   `json:"enabled"`
	HasSecret    bool   `json:"hasSecret"`
	GroupsClaim  string `json:"groupsClaim"`
	CreateGroups bool   `json:"createGroups"`
}

// ssoSignIn points a tool's single sign-on at the realm with the tool's own
// client, so a realm, client or client secret changed later reaches it.
func ssoSignIn(ctx context.Context, s *session, t tool, cfg config, st *state, note func(string, ...any)) error {
	var current struct {
		Provider *oidcProvider `json:"provider"`
		oidcProvider
	}
	err := s.send(ctx, http.MethodGet, "/oidc-provider", nil, &current)
	if err != nil && !hasStatus(err, http.StatusNotFound) {
		return err
	}
	have := current.oidcProvider
	if current.Provider != nil {
		have = *current.Provider
	}
	fingerprint := signInFingerprint(cfg.issuer, t.clientID, t.clientSecret)
	same := have.Issuer == cfg.issuer && have.ClientID == t.clientID && have.Enabled && (have.GroupsClaim == "" || have.GroupsClaim == cfg.groupsClaim)
	if same && have.HasSecret && st.SignIn[t.product] == fingerprint {
		return nil
	}
	body := map[string]any{
		"issuer": cfg.issuer, "clientId": t.clientID, "clientSecret": t.clientSecret,
		"groupsClaim": cfg.groupsClaim, "createGroups": true, "enabled": true,
	}
	if err := s.send(ctx, http.MethodPut, "/oidc-provider", body, nil); err != nil {
		return fmt.Errorf("point %s's sign-in at %s with client %s: %w", displayName[t.product], cfg.issuer, t.clientID, err)
	}
	st.SignIn[t.product] = fingerprint
	if err := st.save(); err != nil {
		return err
	}
	if same {
		note("%s signs in with client %s's current secret", displayName[t.product], t.clientID)
	} else {
		note("%s signs in through %s as client %s", displayName[t.product], cfg.issuer, t.clientID)
	}
	return nil
}

// armatureMembers lets the members in ahead of their first sign-in, which
// Armature matches to them by email; until Cloudster1/Armature#301 a group
// alone admits nobody.
func armatureMembers(ctx context.Context, s *session, members []member, note func(string, ...any)) error {
	for _, m := range members {
		name, _, _ := strings.Cut(m.email, "@")
		body := map[string]string{"email": m.email, "name": name, "role": m.role, "password": randomPassword()}
		err := s.send(ctx, http.MethodPost, "/users", body, nil)
		switch {
		case err == nil:
			note("%s is a member of Armature as %s", m.email, m.role)
		case hasStatus(err, http.StatusConflict):
		default:
			return fmt.Errorf("let %s into Armature: %w", m.email, err)
		}
	}
	return nil
}

// randomPassword is never told to anybody: members sign in through the
// realm, and Armature only refuses an account without one.
func randomPassword() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

type connection struct {
	BaseURL          string   `json:"baseUrl"`
	WebhookSecretSet bool     `json:"webhookSecretSet"`
	WebhookTopics    []string `json:"webhookTopics"`
}

type webhook struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	URL    string   `json:"url"`
	Topics []string `json:"topics"`
	Secret string   `json:"secret"`
}

// connectPair connects the receiver to the sender's address and makes the
// sender deliver the topics the receiver asks for to the receiver's api.
func connectPair(ctx context.Context, receiver *session, rt tool, sender *session, st tool, note func(string, ...any)) error {
	path := "/" + st.product + "/connection"
	var got struct {
		Connection *connection `json:"connection"`
	}
	err := receiver.send(ctx, http.MethodGet, path, nil, &got)
	if err != nil && !hasStatus(err, http.StatusNotFound) {
		return err
	}
	conn := got.Connection
	if conn == nil || conn.BaseURL != st.url {
		var saved struct {
			Connection *connection `json:"connection"`
		}
		if err := receiver.send(ctx, http.MethodPut, path, map[string]any{"baseUrl": st.url}, &saved); err != nil {
			return fmt.Errorf("connect %s to %s at %s: %w", displayName[rt.product], displayName[st.product], st.url, err)
		}
		if saved.Connection == nil {
			return fmt.Errorf("%s saved its connection to %s but did not answer it", displayName[rt.product], displayName[st.product])
		}
		conn = saved.Connection
		note("%s is connected to %s at %s", displayName[rt.product], displayName[st.product], st.url)
	}

	hookURL := strings.TrimRight(rt.api, "/") + "/api/v1" + webhookPath[st.product]
	name := displayName[rt.product]
	var listed struct {
		Webhooks []webhook `json:"webhooks"`
	}
	if err := sender.send(ctx, http.MethodGet, "/webhooks", nil, &listed); err != nil {
		return err
	}
	at := slices.IndexFunc(listed.Webhooks, func(w webhook) bool { return w.Name == name })
	if at >= 0 && conn.WebhookSecretSet && sameWebhook(listed.Webhooks[at], hookURL, conn.WebhookTopics) {
		return nil
	}

	want := map[string]any{"name": name, "url": hookURL, "topics": conn.WebhookTopics, "version": 1}
	var answer struct {
		Webhook webhook `json:"webhook"`
	}
	if at >= 0 {
		id := listed.Webhooks[at].ID
		if err := sender.send(ctx, http.MethodPatch, "/webhooks/"+id, want, nil); err != nil {
			return fmt.Errorf("point %s's webhook for %s at %s: %w", displayName[st.product], name, hookURL, err)
		}
		if err := sender.send(ctx, http.MethodPost, "/webhooks/"+id+"/rotate-secret", nil, &answer); err != nil {
			return fmt.Errorf("give %s's webhook for %s a new secret: %w", displayName[st.product], name, err)
		}
	} else if err := sender.send(ctx, http.MethodPost, "/webhooks", want, &answer); err != nil {
		return fmt.Errorf("make %s's webhook for %s at %s: %w", displayName[st.product], name, hookURL, err)
	}
	if answer.Webhook.Secret == "" {
		return fmt.Errorf("%s made the webhook for %s but answered no secret", displayName[st.product], name)
	}
	body := map[string]any{"baseUrl": st.url, "webhookSecret": answer.Webhook.Secret}
	if err := receiver.send(ctx, http.MethodPut, path, body, nil); err != nil {
		return fmt.Errorf("store %s's webhook secret in %s: %w", displayName[st.product], displayName[rt.product], err)
	}
	note("%s delivers %s to %s", displayName[st.product], strings.Join(conn.WebhookTopics, ", "), hookURL)
	return nil
}

func sameWebhook(w webhook, url string, topics []string) bool {
	return w.URL == url && sameSet(w.Topics, topics)
}

const exchangeCapability = "oauth.token-exchange@1"

// exchangeScopes are what a partner app is given at each provider: what
// Armature's issue links and Stator's and Exciter's writes need.
var exchangeScopes = map[string][]string{
	"armature": {"read", "issue.write"},
	"stator":   {"read", "write"},
	"exciter":  {"read", "write"},
}

type partnerApp struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	KeycloakClientID string   `json:"keycloakClientId"`
	ClientURI        string   `json:"clientUri"`
	RedirectURIs     []string `json:"redirectUris"`
	Scopes           []string `json:"scopes"`
	Status           string   `json:"status"`
	Secret           string   `json:"secret"`
}

// exchangeApp registers the caller as a partner app at the provider and
// stores the app's client id and secret in the caller, so the caller trades a
// person's sign-in for their token at the provider without a click.
func exchangeApp(ctx context.Context, caller *session, ct tool, provider *session, pt tool, note func(string, ...any)) error {
	held, err := heldApp(ctx, caller, ct, pt)
	if err != nil {
		return err
	}
	var listed struct {
		Clients []partnerApp `json:"clients"`
	}
	if err := provider.send(ctx, http.MethodGet, "/oauth/clients", nil, &listed); err != nil {
		return err
	}
	name := displayName[ct.product]
	at := -1
	if held != "" {
		at = slices.IndexFunc(listed.Clients, func(a partnerApp) bool { return a.ID == held })
	}
	if at < 0 {
		at = slices.IndexFunc(listed.Clients, func(a partnerApp) bool { return a.Name == name })
	}
	want := wantedApp(ct, pt)
	what := fmt.Sprintf("%s's partner app in %s", name, displayName[pt.product])

	var app partnerApp
	if at >= 0 {
		app = listed.Clients[at]
		if patch := appChanges(app, want, pt.product); len(patch) > 0 {
			if err := provider.send(ctx, http.MethodPatch, "/oauth/clients/"+app.ID, patch, nil); err != nil {
				return fmt.Errorf("update %s: %w", what, err)
			}
			note("%s has a new %s", what, strings.Join(slices.Sorted(maps.Keys(patch)), ", "))
		}
		if app.Status == "pending" {
			if err := provider.send(ctx, http.MethodPost, "/oauth/clients/"+app.ID+"/approve", nil, nil); err != nil {
				return fmt.Errorf("allow %s: %w", what, err)
			}
			note("%s is allowed", what)
		}
		if app.ID == held {
			return nil
		}
	}

	// A secret is shown only when it is made, so the caller gets a new one.
	var answer struct {
		Client partnerApp `json:"client"`
	}
	switch {
	case at >= 0 && pt.product != "stator":
		if err := provider.send(ctx, http.MethodPost, "/oauth/clients/"+app.ID+"/rotate-secret", nil, &answer); err != nil {
			return fmt.Errorf("give %s a new secret: %w", what, err)
		}
	default:
		if at >= 0 {
			// Stator issues no new secret to an app, so its app is made anew.
			if err := provider.send(ctx, http.MethodDelete, "/oauth/clients/"+app.ID, nil, nil); err != nil {
				return fmt.Errorf("remove %s, whose secret %s lost: %w", what, displayName[ct.product], err)
			}
		}
		if err := provider.send(ctx, http.MethodPost, "/oauth/clients", appBody(want, pt.product), &answer); err != nil {
			return fmt.Errorf("register %s: %w", what, err)
		}
	}
	if answer.Client.ID == "" || answer.Client.Secret == "" {
		return fmt.Errorf("%s registered %s but answered no client id and secret", displayName[pt.product], what)
	}

	creds := map[string]any{"clientId": answer.Client.ID, "clientSecret": answer.Client.Secret}
	path := "/" + pt.product + "/connection/app"
	if ct.product == "exciter" {
		path = "/" + pt.product + "/connection"
		creds["baseUrl"] = pt.url
	}
	if err := caller.send(ctx, http.MethodPut, path, creds, nil); err != nil {
		return fmt.Errorf("store %s in %s: %w", what, displayName[ct.product], err)
	}
	note("%s connects people to %s at sign-in, as %s", displayName[ct.product], displayName[pt.product], what)
	return nil
}

// heldApp answers the client id of the caller's app at the provider when the
// caller holds its secret too, and nothing otherwise.
func heldApp(ctx context.Context, caller *session, ct tool, pt tool) (string, error) {
	var got struct {
		Connection *struct {
			ClientID        string `json:"clientId"`
			ClientSecretSet bool   `json:"clientSecretSet"`
			App             *struct {
				ClientID     *string `json:"clientId"`
				SecretSet    bool    `json:"secretSet"`
				RegisteredBy *string `json:"registeredBy"`
				Status       string  `json:"status"`
			} `json:"app"`
		} `json:"connection"`
	}
	if err := caller.send(ctx, http.MethodGet, "/"+pt.product+"/connection", nil, &got); err != nil {
		return "", err
	}
	c := got.Connection
	switch {
	case c == nil:
		return "", nil
	case ct.product == "exciter":
		if c.ClientSecretSet {
			return c.ClientID, nil
		}
	case c.App != nil && c.App.ClientID != nil:
		// Stator says of its Armature app who registered it rather than whether
		// a secret is stored: one an administrator stored is active with one.
		if c.App.SecretSet || (c.App.RegisteredBy != nil && *c.App.RegisteredBy == "admin" && c.App.Status == "active") {
			return *c.App.ClientID, nil
		}
	}
	return "", nil
}

func wantedApp(ct, pt tool) partnerApp {
	url := strings.TrimRight(ct.url, "/")
	app := partnerApp{Name: displayName[ct.product], KeycloakClientID: ct.clientID, Scopes: exchangeScopes[pt.product]}
	if pt.product != "stator" {
		app.ClientURI = url
	}
	if pt.product == "armature" {
		app.RedirectURIs = []string{url + "/api/v1/armature/oauth/callback"}
	}
	return app
}

// appBody is the registration each provider takes; Armature alone keeps
// redirect addresses and Stator no client address.
func appBody(want partnerApp, provider string) map[string]any {
	body := map[string]any{"name": want.Name, "keycloakClientId": want.KeycloakClientID, "scopes": want.Scopes}
	if provider != "stator" {
		body["clientUri"] = want.ClientURI
	}
	if provider == "armature" {
		body["redirectUris"] = want.RedirectURIs
		body["confidential"] = true
	}
	return body
}

func appChanges(have, want partnerApp, provider string) map[string]any {
	patch := map[string]any{}
	if have.Name != want.Name {
		patch["name"] = want.Name
	}
	if have.KeycloakClientID != want.KeycloakClientID {
		patch["keycloakClientId"] = want.KeycloakClientID
	}
	if !sameSet(have.Scopes, want.Scopes) {
		patch["scopes"] = want.Scopes
	}
	if provider != "stator" && have.ClientURI != want.ClientURI {
		patch["clientUri"] = want.ClientURI
	}
	if provider == "armature" && !sameSet(have.RedirectURIs, want.RedirectURIs) {
		patch["redirectUris"] = want.RedirectURIs
	}
	return patch
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
