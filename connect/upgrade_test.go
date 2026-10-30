package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// upgradeSnapshot is what make test-upgrade records before the upgrade and
// holds the upgraded stack to, kept as JSON between the two go test runs.
type upgradeSnapshot struct {
	Versions map[string]string `json:"versions"`
	// Content holds, per kind of thing, one line per item or link.
	Content map[string][]string `json:"content"`
	// Access holds, per person, what each tool answered when asked what they may do.
	Access map[string]map[string]reading `json:"access"`
	// ArmaturePermissions are the permission names Armature knew before.
	ArmaturePermissions []string `json:"armaturePermissions"`
	// Refs name the page, requirement and issue every person was asked about.
	Refs map[string]string `json:"refs"`
}

// reading is one answer: its status, and the part of its body that says what the person may do.
type reading struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// lost answers the lines of before that after no longer has.
func lost(before, after []string) []string {
	var out []string
	for _, line := range before {
		if !slices.Contains(after, line) {
			out = append(out, line)
		}
	}
	return out
}

// readingChanges says how an answer after the upgrade differs from the one
// before, comparing only what both versions report.
func readingChanges(before, after reading, known map[string]bool) []string {
	if before.Status != after.Status {
		return []string{fmt.Sprintf("was answered %d, now %d", before.Status, after.Status)}
	}
	if len(before.Body) == 0 || len(after.Body) == 0 {
		return nil
	}
	var b, a any
	if err := json.Unmarshal(before.Body, &b); err != nil {
		return []string{"the recorded answer is not JSON: " + err.Error()}
	}
	if err := json.Unmarshal(after.Body, &a); err != nil {
		return []string{"the answer is not JSON: " + err.Error()}
	}
	return accessChanges(b, a, "", known)
}

// accessChanges compares two decoded answers. A field only one of them has
// is left out, since a version may report more or less; known, when set,
// narrows lists under a field called permissions to the names both versions
// know.
func accessChanges(before, after any, at string, known map[string]bool) []string {
	name := at
	if name == "" {
		name = "the answer"
	}
	switch b := before.(type) {
	case map[string]any:
		a, ok := after.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s was an object, now %s", name, compact(after))}
		}
		var out []string
		for _, k := range slices.Sorted(maps.Keys(b)) {
			if av, ok := a[k]; ok {
				out = append(out, accessChanges(b[k], av, strings.TrimPrefix(at+"."+k, "."), known)...)
			}
		}
		return out
	case []any:
		a, ok := after.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s was a list, now %s", name, compact(after))}
		}
		bs, bok := allStrings(b)
		as, aok := allStrings(a)
		if !bok || !aok {
			if !sameMembers(b, a) {
				return []string{fmt.Sprintf("%s was %s, now %s", name, compact(before), compact(after))}
			}
			return nil
		}
		if known != nil && (at == "permissions" || strings.HasPrefix(at, "permissions.") || strings.HasSuffix(at, ".permissions")) {
			bs, as = only(bs, known), only(as, known)
		}
		var out []string
		if gone := lost(bs, as); len(gone) > 0 {
			out = append(out, fmt.Sprintf("%s lost %s", name, strings.Join(gone, ", ")))
		}
		if added := lost(as, bs); len(added) > 0 {
			out = append(out, fmt.Sprintf("%s gained %s", name, strings.Join(added, ", ")))
		}
		return out
	default:
		if !reflect.DeepEqual(before, after) {
			return []string{fmt.Sprintf("%s was %s, now %s", name, compact(before), compact(after))}
		}
		return nil
	}
}

func allStrings(list []any) ([]string, bool) {
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func only(list []string, known map[string]bool) []string {
	var out []string
	for _, s := range list {
		if known[s] {
			out = append(out, s)
		}
	}
	return out
}

// sameMembers compares two lists as sets, whatever their order.
func sameMembers(a, b []any) bool {
	set := func(list []any) []string {
		out := make([]string, 0, len(list))
		for _, v := range list {
			out = append(out, compact(v))
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return slices.Equal(set(a), set(b))
}

func compact(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

func TestTheSameAccessReportsNoChange(t *testing.T) {
	before := reading{Status: 200, Body: json.RawMessage(`{"editPages":true,"administer":false,"labels":["a","b"]}`)}
	after := reading{Status: 200, Body: json.RawMessage(`{"labels":["b","a"],"administer":false,"editPages":true,"purge":true}`)}
	if got := readingChanges(before, after, nil); len(got) != 0 {
		t.Errorf("the same rights in another order with a new field were reported as %v", got)
	}
}

func TestAccessLostOrGainedIsNamed(t *testing.T) {
	before := reading{Status: 200, Body: json.RawMessage(`{"can":{"editPages":true,"delete":false}}`)}
	after := reading{Status: 200, Body: json.RawMessage(`{"can":{"editPages":false,"delete":true}}`)}
	got := readingChanges(before, after, nil)
	want := []string{"can.delete was false, now true", "can.editPages was true, now false"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := readingChanges(reading{Status: 200}, reading{Status: 403}, nil); !slices.Equal(got, []string{"was answered 200, now 403"}) {
		t.Errorf("a refusal after the upgrade was reported as %q", got)
	}
}

func TestOnlyPermissionsBothVersionsKnowAreCompared(t *testing.T) {
	before := reading{Status: 200, Body: json.RawMessage(`{"permissions":{"org":["read"],"projects":{"UPG":["read","issue.write"]}},"projects":["UPG"]}`)}
	after := reading{Status: 200, Body: json.RawMessage(`{"permissions":{"org":["read","issue.reveal"],"projects":{"UPG":["read","issue.write","issue.reveal"]}},"projects":["UPG"]}`)}
	known := map[string]bool{"read": true, "issue.write": true}
	if got := readingChanges(before, after, known); len(got) != 0 {
		t.Errorf("a permission only the new version knows was reported: %v", got)
	}
	after.Body = json.RawMessage(`{"permissions":{"org":["read"],"projects":{"UPG":["read"]}},"projects":[]}`)
	got := readingChanges(before, after, known)
	want := []string{"permissions.projects.UPG lost issue.write", "projects lost UPG"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLostNamesWhatIsGone(t *testing.T) {
	got := lost([]string{"UPG-1 Login", "UPG-2 Logout"}, []string{"UPG-2 Logout", "UPG-3 New"})
	if !slices.Equal(got, []string{"UPG-1 Login"}) {
		t.Errorf("got %q", got)
	}
}
