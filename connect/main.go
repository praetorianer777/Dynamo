// Command dynamo-connect connects Stator, Armature and Exciter to each other
// through their admin APIs, once all three answer, and exits. Running it again
// changes only what differs. dynamo-connect sample-data instead fills them
// with sample projects from GitHub or a built-in snapshot.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// tool is one of the three, as the job reaches it and as people do.
type tool struct {
	product string
	// api is the in-network address; url the one browsers and partners are told.
	api, url        string
	version         string
	email, password string
	// The tool's client in the realm, which its sign-in uses.
	clientID, clientSecret string
}

type config struct {
	stator, armature, exciter tool
	issuer                    string
	groupsClaim               string
	members                   []member
	expectUnchanged           bool
	waitFor                   time.Duration
	// Where dynamo-connect remembers what it sent; it must outlive the run.
	stateDir string
}

// member is somebody of the realm let into every tool before their first sign-in.
type member struct{ email, role string }

const defaultWait = 5 * time.Minute

func main() {
	cfg, err := load(os.Getenv)
	if err != nil {
		slog.Error("dynamo-connect is not set up", "error", err)
		os.Exit(2)
	}
	ctx := context.Background()
	if len(os.Args) > 1 && os.Args[1] == "sample-data" {
		sampleData(ctx, cfg)
		return
	}
	changes, err := run(ctx, cfg)
	for _, c := range changes {
		slog.Info("changed", "what", c)
	}
	if err != nil {
		slog.Error("the tools could not be connected", "error", err)
		os.Exit(1)
	}
	if len(changes) == 0 {
		slog.Info("the tools were connected already; nothing changed")
	}
	if cfg.expectUnchanged && len(changes) > 0 {
		slog.Error("a second run changed something, so the first did not leave the tools connected", "changes", len(changes))
		os.Exit(3)
	}
}

// sampleData is dynamo-connect sample-data: the sources DYNAMO_SAMPLE_SOURCES
// names, imported into the three tools, after they are connected.
func sampleData(ctx context.Context, cfg config) {
	sc, err := loadSample(os.Getenv)
	if err != nil {
		slog.Error("the sample data is not set up", "error", err)
		os.Exit(2)
	}
	changes, warnings, err := runSample(ctx, cfg, sc)
	for _, c := range changes {
		slog.Info("imported", "what", c)
	}
	for _, w := range warnings {
		slog.Warn("left out", "what", w)
	}
	if err != nil {
		slog.Error("the sample data could not be imported", "error", err)
		os.Exit(1)
	}
	if len(changes) == 0 {
		slog.Info("the sample data was imported already; nothing changed")
	}
	if cfg.expectUnchanged && len(changes) > 0 {
		slog.Error("a second import changed something, so the first did not finish it", "changes", len(changes))
		os.Exit(3)
	}
}

func load(getenv func(string) string) (config, error) {
	var missing []string
	need := func(key string) string {
		v := getenv(key)
		if v == "" {
			missing = append(missing, key)
		}
		return v
	}
	read := func(product, prefix string) tool {
		return tool{
			product:      product,
			api:          need(prefix + "_API"),
			url:          need(prefix + "_URL"),
			version:      getenv(prefix + "_VERSION"),
			email:        need(prefix + "_ADMIN_EMAIL"),
			password:     need(prefix + "_ADMIN_PASSWORD"),
			clientID:     need(prefix + "_OIDC_CLIENT_ID"),
			clientSecret: need(prefix + "_OIDC_CLIENT_SECRET"),
		}
	}
	cfg := config{
		stator:          read("stator", "DYNAMO_STATOR"),
		armature:        read("armature", "DYNAMO_ARMATURE"),
		exciter:         read("exciter", "DYNAMO_EXCITER"),
		issuer:          need("DYNAMO_OIDC_ISSUER"),
		groupsClaim:     getenv("DYNAMO_OIDC_GROUPS_CLAIM"),
		expectUnchanged: getenv("DYNAMO_EXPECT_UNCHANGED") == "1",
		waitFor:         defaultWait,
		stateDir:        getenv("DYNAMO_STATE_DIR"),
	}
	if cfg.stateDir == "" {
		cfg.stateDir = defaultStateDir
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("set %s", strings.Join(missing, ", "))
	}
	if w := getenv("DYNAMO_WAIT"); w != "" {
		d, err := time.ParseDuration(w)
		if err != nil {
			return cfg, fmt.Errorf("DYNAMO_WAIT is a duration such as 5m, not %q", w)
		}
		cfg.waitFor = d
	}
	if cfg.groupsClaim == "" {
		cfg.groupsClaim = "groups"
	}
	members, err := parseMembers(getenv("DYNAMO_MEMBERS"))
	if err != nil {
		return cfg, err
	}
	cfg.members = members
	return cfg, nil
}

// parseMembers reads email=role pairs, the form Stator's and Exciter's
// bootstrap settings take too.
func parseMembers(s string) ([]member, error) {
	var out []member
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		email, role, ok := strings.Cut(pair, "=")
		if !ok || !strings.Contains(email, "@") || (role != "admin" && role != "member") {
			return nil, fmt.Errorf("DYNAMO_MEMBERS is email=admin or email=member, separated by commas; %q is neither", pair)
		}
		out = append(out, member{email: email, role: role})
	}
	return out, nil
}
