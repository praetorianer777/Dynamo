//go:build stack

// The checks on a running stack, compose or the chart in kind: they need
// DYNAMO_STACK_ENV, a file of NAME=value lines with the tools' addresses and
// administrators, and with the chart DYNAMO_HOSTS, host:port=address pairs
// that send each Service name to its port-forward.
package main

import (
	"bufio"
	"context"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func stackEnv(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(os.Getenv("DYNAMO_STACK_ENV"))
	if err != nil {
		t.Fatalf("the stack is not up (%v); run make up first", err)
	}
	defer f.Close()
	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			env[k] = v
		}
	}
	return env
}

// browser is a client that, like a person's browser, keeps cookies and
// follows redirects, and reaches the stack's names where DYNAMO_HOSTS says.
func browser() *http.Client {
	hosts := map[string]string{}
	for _, pair := range strings.Split(os.Getenv("DYNAMO_HOSTS"), ",") {
		if name, addr, ok := strings.Cut(pair, "="); ok {
			hosts[name] = addr
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		if to, ok := hosts[addr]; ok {
			addr = to
		}
		return dialer.DialContext(ctx, network, addr)
	}}
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 30 * time.Second, Transport: transport}
}

var loginForm = regexp.MustCompile(`<form[^>]*id="kc-form-login"[^>]*action="([^"]+)"`)

var products = []string{"stator", "armature", "exciter"}

// TestStackOneSignInThroughTheRealmOpensAllThree signs alice in to Stator with
// her password at Keycloak, then into Armature and Exciter on the same browser
// session, where Keycloak must not ask again.
func TestStackOneSignInThroughTheRealmOpensAllThree(t *testing.T) {
	env := stackEnv(t)
	b := browser()
	const email, password = "alice@suite.test", "alice password"

	for i, product := range products {
		base := env[strings.ToUpper(product)+"_URL"]
		resp, err := b.Get(base + "/api/v1/auth/oidc/start")
		if err != nil {
			t.Fatalf("%s: start sign-in: %v", product, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		form := loginForm.FindSubmatch(body)
		switch {
		case i == 0 && form == nil:
			t.Fatalf("%s: Keycloak showed no login form at %s (status %d)", product, resp.Request.URL, resp.StatusCode)
		case i > 0 && form != nil:
			t.Fatalf("%s: Keycloak asked for the password again, so the sign-in to Stator does not carry over", product)
		case form != nil:
			resp, err = b.PostForm(html.UnescapeString(string(form[1])), url.Values{"username": {email}, "password": {password}, "credentialId": {""}})
			if err != nil {
				t.Fatalf("%s: post the password to Keycloak: %v", product, err)
			}
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			if loginForm.Match(body) {
				t.Fatalf("%s: Keycloak refused alice's password", product)
			}
		}
		if !strings.HasPrefix(resp.Request.URL.String(), base) {
			t.Fatalf("%s: sign-in ended at %s, not back at %s", product, resp.Request.URL, base)
		}

		me, err := b.Get(base + "/api/v1/auth/me")
		if err != nil {
			t.Fatalf("%s: ask who is signed in: %v", product, err)
		}
		raw, _ := io.ReadAll(me.Body)
		me.Body.Close()
		if me.StatusCode != http.StatusOK || !strings.Contains(string(raw), email) {
			t.Fatalf("%s: /auth/me answered %d after signing in, not naming %s: %s", product, me.StatusCode, email, raw)
		}
		t.Logf("%s: signed in as %s", product, email)
	}
}

// TestStackEveryWebhookDelivers has each tool send a test delivery on every
// webhook dynamo-connect made, which the receiver only accepts when the
// secret it was given signs it.
func TestStackEveryWebhookDelivers(t *testing.T) {
	env := stackEnv(t)
	ctx := context.Background()
	want := map[string][]string{"armature": {"Stator", "Exciter"}, "stator": {"Exciter"}, "exciter": {"Stator"}}
	for _, product := range products {
		P := strings.ToUpper(product)
		s := newSession(product, env[P+"_URL"])
		s.client = browser()
		if err := s.signIn(ctx, env[P+"_ADMIN_EMAIL"], env[P+"_ADMIN_PASSWORD"]); err != nil {
			t.Fatal(err)
		}
		var listed struct {
			Webhooks []webhook `json:"webhooks"`
		}
		if err := s.send(ctx, http.MethodGet, "/webhooks", nil, &listed); err != nil {
			t.Fatal(err)
		}
		for _, name := range want[product] {
			var hook *webhook
			for i := range listed.Webhooks {
				if listed.Webhooks[i].Name == name {
					hook = &listed.Webhooks[i]
				}
			}
			if hook == nil {
				t.Errorf("%s has no webhook for %s", product, name)
				continue
			}
			if err := s.send(ctx, http.MethodPost, "/webhooks/"+hook.ID+"/test", nil, nil); err != nil {
				t.Errorf("%s: test the webhook for %s: %v", product, name, err)
				continue
			}
			if status := awaitDelivery(ctx, s, hook.ID); status < 200 || status > 299 {
				t.Errorf("%s's test delivery to %s at %s was answered %d", product, name, hook.URL, status)
			} else {
				t.Logf("%s -> %s: %d", product, name, status)
			}
		}
	}
}

// awaitDelivery answers the status of the endpoint's newest answered delivery.
func awaitDelivery(ctx context.Context, s *session, id string) int {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var got struct {
			Deliveries []struct {
				Status int `json:"status"`
			} `json:"deliveries"`
		}
		if err := s.send(ctx, http.MethodGet, "/webhooks/"+id+"/deliveries", nil, &got); err == nil {
			for _, d := range got.Deliveries {
				if d.Status != 0 {
					return d.Status
				}
			}
		}
		time.Sleep(time.Second)
	}
	return 0
}
