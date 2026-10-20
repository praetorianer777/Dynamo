//go:build stack

package main

import (
	"bufio"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The running stack's addresses, as make stack-env writes them.
func stackEnv(t *testing.T) map[string]string {
	t.Helper()
	path := os.Getenv("DYNAMO_STACK_ENV")
	f, err := os.Open(path)
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

var loginForm = regexp.MustCompile(`<form[^>]*id="kc-form-login"[^>]*action="([^"]+)"`)

// TestStackOneSignInThroughTheRealmOpensAllThree signs alice in to Stator with her
// password at Keycloak, then into Armature and Exciter on the same browser
// session, where Keycloak must not ask again.
func TestStackOneSignInThroughTheRealmOpensAllThree(t *testing.T) {
	env := stackEnv(t)
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	const email, password = "alice@suite.test", "alice password"

	for i, product := range []string{"stator", "armature", "exciter"} {
		base := env[strings.ToUpper(product)+"_URL"]
		resp, err := browser.Get(base + "/api/v1/auth/oidc/start")
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
			resp, err = browser.PostForm(html.UnescapeString(string(form[1])), url.Values{"username": {email}, "password": {password}, "credentialId": {""}})
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

		me, err := browser.Get(base + "/api/v1/auth/me")
		if err != nil {
			t.Fatalf("%s: ask who is signed in: %v", product, err)
		}
		raw, _ := io.ReadAll(me.Body)
		me.Body.Close()
		if me.StatusCode != http.StatusOK {
			t.Fatalf("%s: /auth/me answered %d after signing in: %s", product, me.StatusCode, raw)
		}
		if !strings.Contains(string(raw), email) {
			t.Fatalf("%s: signed in, but not as %s: %s", product, email, raw)
		}
		t.Logf("%s: signed in as %s", product, email)
	}
}
