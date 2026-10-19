package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
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

	for _, t := range []tool{cfg.stator, cfg.armature, cfg.exciter} {
		if err := awaitMeta(ctx, t, cfg.waitFor); err != nil {
			return changes, err
		}
	}

	armature, err := armatureOwner(ctx, cfg.armature, note)
	if err != nil {
		return changes, err
	}
	if err := armatureSignIn(ctx, armature, cfg, note); err != nil {
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

	// Each receiver connects to its sender, then the sender gets the receiver's
	// webhook, whose secret goes back to the receiver.
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
func awaitMeta(ctx context.Context, t tool, waitFor time.Duration) error {
	s := newSession(t.product, t.api)
	deadline := time.Now().Add(waitFor)
	for {
		var m meta
		err := s.send(ctx, http.MethodGet, "/meta", nil, &m)
		if err == nil {
			if !strings.EqualFold(m.Product, t.product) {
				return fmt.Errorf("%s answers as %q; DYNAMO_%s_API names another tool", t.api, m.Product, strings.ToUpper(t.product))
			}
			if t.version != "" && m.Build.Version != t.version {
				return fmt.Errorf("%s runs %s, but versions.yaml pins %s; pull the pinned image or change the pin", displayName[t.product], m.Build.Version, t.version)
			}
			slog.Info("answering", "tool", t.product, "version", m.Build.Version)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not answer GET /api/v1/meta within %s: %w", displayName[t.product], waitFor, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
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
	GroupsClaim  string `json:"groupsClaim"`
	CreateGroups bool   `json:"createGroups"`
}

// armatureSignIn points Armature's sign-in at the shared realm; Stator and
// Exciter have theirs from their own bootstrap settings.
func armatureSignIn(ctx context.Context, s *session, cfg config, note func(string, ...any)) error {
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
	if have.Issuer == cfg.issuer && have.ClientID == cfg.armatureClientID && have.Enabled {
		return nil
	}
	body := map[string]any{
		"issuer": cfg.issuer, "clientId": cfg.armatureClientID, "clientSecret": cfg.armatureClientSecret,
		"groupsClaim": "groups", "createGroups": true, "enabled": true,
	}
	if err := s.send(ctx, http.MethodPut, "/oidc-provider", body, nil); err != nil {
		return fmt.Errorf("point Armature's sign-in at %s: %w", cfg.issuer, err)
	}
	note("Armature signs in through %s", cfg.issuer)
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
	a, b := slices.Clone(w.Topics), slices.Clone(topics)
	slices.Sort(a)
	slices.Sort(b)
	return w.URL == url && slices.Equal(a, b)
}
