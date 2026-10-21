//go:build stack

// The checks on a running stack, compose or the chart: the tools' addresses
// and administrators come as NAME=value lines from the file DYNAMO_STACK_ENV
// names, or without it from the environment, as helm test gives them. With
// DYNAMO_HOSTS, host:port=address pairs, a Service name is sent elsewhere.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"html"
	"io"
	"maps"
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
	env := map[string]string{}
	name := os.Getenv("DYNAMO_STACK_ENV")
	if name == "" {
		for _, kv := range os.Environ() {
			if k, v, ok := strings.Cut(kv, "="); ok {
				env[k] = v
			}
		}
		if env["STATOR_URL"] == "" {
			t.Fatal("STATOR_URL is not set: give the addresses in the environment or a file in DYNAMO_STACK_ENV")
		}
		return env
	}
	f, err := os.Open(name)
	if err != nil {
		t.Fatalf("the stack is not up (%v); run make up first", err)
	}
	defer f.Close()
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

// TestStackOneSignInThroughTheRealmOpensAllThree signs a person in to Stator
// with their password at Keycloak, then into Armature and Exciter on the same
// browser session, where Keycloak must not ask again. The person is the demo
// realm's alice unless SIGNIN_EMAIL names another; set empty, it is skipped.
func TestStackOneSignInThroughTheRealmOpensAllThree(t *testing.T) {
	env := stackEnv(t)
	b := browser()
	email, named := env["SIGNIN_EMAIL"]
	password := env["SIGNIN_PASSWORD"]
	if !named {
		email, password = "alice@suite.test", "alice password"
	}
	if email == "" {
		t.Skip("no account to sign in with: set SIGNIN_EMAIL and SIGNIN_PASSWORD to check the sign-in")
	}

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
				t.Fatalf("%s: Keycloak refused %s's password", product, email)
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

// TestStackTheSampleIsInAllThree checks what the sample-data mode made of the
// built-in sparkplug, when DYNAMO_SAMPLE_SOURCES names it: its issues in
// Armature, its docs in Stator, its features in Exciter, and the links between them.
func TestStackTheSampleIsInAllThree(t *testing.T) {
	env := stackEnv(t)
	if !strings.Contains(env["DYNAMO_SAMPLE_SOURCES"]+os.Getenv("DYNAMO_SAMPLE_SOURCES"), "sparkplug") {
		t.Skip("DYNAMO_SAMPLE_SOURCES does not name sparkplug")
	}
	ctx := context.Background()
	im := &importer{armatureURL: strings.TrimRight(env["ARMATURE_URL"], "/"), statorURL: strings.TrimRight(env["STATOR_URL"], "/"),
		exciterURL: strings.TrimRight(env["EXCITER_URL"], "/")}
	for _, s := range []struct {
		s       **session
		product string
	}{{&im.armature, "armature"}, {&im.stator, "stator"}, {&im.exciter, "exciter"}} {
		P := strings.ToUpper(s.product)
		*s.s = newSession(s.product, env[P+"_URL"])
		(*s.s).client = browser()
		if err := (*s.s).signIn(ctx, env[P+"_ADMIN_EMAIL"], env[P+"_ADMIN_PASSWORD"]); err != nil {
			t.Fatal(err)
		}
	}

	var issues struct {
		Issues []struct {
			Key     string `json:"key"`
			Summary string `json:"summary"`
			Type    struct {
				Name string `json:"name"`
			} `json:"type"`
		} `json:"issues"`
	}
	if err := im.armature.send(ctx, http.MethodGet, "/projects/SPARK/issues?limit=100", nil, &issues); err != nil {
		t.Fatal(err)
	}
	keys, kinds := map[int]string{}, map[int]string{}
	for _, i := range issues.Issues {
		if n, ok := numberOf(i.Summary); ok {
			keys[n], kinds[n] = i.Key, i.Type.Name
		}
	}
	if len(keys) != 4 {
		t.Fatalf("Armature's SPARK has the sample issues %v, want #1 to #4", keys)
	}
	if want := map[int]string{1: "Story", 2: "Bug", 3: "Story", 4: "Task"}; !maps.Equal(kinds, want) {
		t.Errorf("the sample issues have the types %v, want %v from their labels", kinds, want)
	}
	links := func(s *session, path string) string {
		t.Helper()
		var raw json.RawMessage
		if err := s.send(ctx, http.MethodGet, path, nil, &raw); err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if got := links(im.armature, "/issues/"+keys[1]+"/remote-links"); !strings.Contains(got, "github.com/dynamo-sample/sparkplug/issues/1") || !strings.Contains(got, im.statorURL+"/p/") {
		t.Errorf("%s links to %s, want its GitHub issue and the Stator pages that mention it", keys[1], got)
	}
	if got := links(im.armature, "/issues/"+keys[2]); !strings.Contains(got, "good-first-issue") {
		t.Errorf("%s lacks the label good first issue in Armature's form: %s", keys[2], got)
	}
	for _, n := range []int{2, 3} {
		if got := links(im.armature, "/issues/"+keys[n]+"/links"); !strings.Contains(got, `"key":"`+keys[1]+`"`) {
			t.Errorf("%s relates to %s, want %s, which it mentions", keys[n], got, keys[1])
		}
	}

	var outline struct {
		Pages []struct {
			ID       string `json:"id"`
			ParentID string `json:"parentId"`
			Title    string `json:"title"`
		} `json:"pages"`
	}
	if err := im.stator.send(ctx, http.MethodGet, "/spaces/SPARK/outline", nil, &outline); err != nil {
		t.Fatal(err)
	}
	pages, parents := map[string]string{}, map[string]string{}
	for _, pg := range outline.Pages {
		pages[pg.Title], parents[pg.ID] = pg.ID, pg.ParentID
	}
	for _, title := range []string{"Sparkplug", "Docs", "Configuration", "MQTT topics", "docs.sparkplug.example", "Installing Sparkplug", "Questions", "Changelog", "About this sample"} {
		if pages[title] == "" {
			t.Errorf("Stator's SPARK has no page %q among %v", title, pages)
		}
	}
	notes := 0
	for _, pg := range outline.Pages {
		if pg.Title == "Notes" {
			notes++
		}
	}
	if notes != 2 {
		t.Errorf("Stator's SPARK has %d pages called Notes, want the two of the repository and the site, once each", notes)
	}
	for page, folder := range map[string]string{"Configuration": "Docs", "Installing Sparkplug": "docs.sparkplug.example", "Docs": "Sparkplug"} {
		if parents[pages[page]] != pages[folder] {
			t.Errorf("the page %s is not under %s", page, folder)
		}
	}
	text := func(title string) string {
		t.Helper()
		var md []byte
		if err := im.stator.send(ctx, http.MethodGet, "/pages/"+pages[title]+"/markdown", nil, &md); err != nil {
			t.Fatal(err)
		}
		return string(md)
	}
	page := func(title string) string { return im.statorURL + "/p/" + pages[title] }
	readme := text("Sparkplug")
	for _, want := range []string{page("Configuration"), page("Installing Sparkplug"), im.armatureURL + "/issues/" + keys[2], "https://docs.sparkplug.example/en/docs/devices/chargers"} {
		if !strings.Contains(readme, want) {
			t.Errorf("the README does not link to %s:\n%s", want, readme)
		}
	}
	if install := text("Installing Sparkplug"); !strings.Contains(install, page("Configuration")) {
		t.Errorf("the site's page does not link to the configuration's page:\n%s", install)
	}
	if faq := text("Questions"); !strings.Contains(faq, page("Installing Sparkplug")) {
		t.Errorf("the site's page does not link to the site's other page by its path:\n%s", faq)
	}
	if topics := text("MQTT topics"); !strings.Contains(topics, page("Configuration")+"#topics") {
		t.Errorf("the topics do not link to the configuration's page, relative as they are:\n%s", topics)
	}
	if folder := text("Docs"); !strings.Contains(folder, page("Configuration")) {
		t.Errorf("the folder's page does not list the pages under it:\n%s", folder)
	}
	if id := pages["Configuration"]; id != "" {
		if got := links(im.stator, "/pages/"+id+"/remote-links"); !strings.Contains(got, im.armatureURL+"/issues/"+keys[4]) || !strings.Contains(got, "https://github.com/dynamo-sample/sparkplug/blob/HEAD/docs/configuration.md") {
			t.Errorf("the page Configuration links to %s, want %s, which it mentions, and its source", got, keys[4])
		}
	}
	if id := pages["About this sample"]; id != "" {
		got := links(im.stator, "/pages/"+id+"/remote-links")
		for _, want := range []string{im.armatureURL + "/projects/SPARK", im.exciterURL + "/p/SPARK"} {
			if !strings.Contains(got, want) {
				t.Errorf("the page About this sample links to %s, want %s", got, want)
			}
		}
	}

	var reqs struct {
		Rows []struct {
			Key   string `json:"key"`
			Title string `json:"title"`
		} `json:"rows"`
	}
	if err := im.exciter.send(ctx, http.MethodGet, "/projects/SPARK/requirements?limit=100", nil, &reqs); err != nil {
		t.Fatal(err)
	}
	features := map[int]string{}
	for _, r := range reqs.Rows {
		if n, ok := numberOf(r.Title); ok {
			features[n] = r.Key
		}
	}
	if len(features) != 2 || features[1] == "" || features[3] == "" {
		t.Fatalf("Exciter's SPARK has the requirements %v, want one each for the features #1 and #3", features)
	}
	if got := links(im.exciter, "/requirements/"+features[1]+"/armature-links"); !strings.Contains(got, `"issueKey":"`+keys[1]+`"`) {
		t.Errorf("%s is traced to %s, want %s", features[1], got, keys[1])
	}
	if got := links(im.exciter, "/requirements/"+features[1]+"/stator-links"); !strings.Contains(got, pages["Changelog"]) {
		t.Errorf("%s is traced to %s, want the page Changelog, which mentions #1", features[1], got)
	}
	if got := links(im.exciter, "/requirements/"+features[3]+"/links"); !strings.Contains(got, `"key":"`+features[1]+`"`) {
		t.Errorf("%s relates to %s, want %s, whose issue it mentions", features[3], got, features[1])
	}
}
