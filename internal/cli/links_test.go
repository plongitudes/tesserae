package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestLinkPrintsTheLinkInItsCanonicalDirectionForEveryWording(t *testing.T) {
	inInitialisedRepo(t)
	a, b := newID(t, "A"), newID(t, "B")
	// A blocks link and a parent link can't join the same two issues
	// (ADR-0016), so the parent wordings have a pair of their own.
	p, c := newID(t, "P"), newID(t, "C")
	for _, tc := range []struct {
		args []string
		want object
	}{
		{[]string{a, "blocks", b}, object{"from": a, "type": "blocks", "to": b}},
		{[]string{b, "blocked-by", a}, object{"from": a, "type": "blocks", "to": b}},
		{[]string{p, "parent-of", c}, object{"from": p, "type": "parent", "to": c}},
		{[]string{c, "child-of", p}, object{"from": p, "type": "parent", "to": c}},
		{[]string{a, "related", b}, object{"from": a, "type": "related", "to": b}},
		{[]string{b, "discovered-from", a}, object{"from": b, "type": "discovered-from", "to": a}},
	} {
		if got := ok[[]any](t, append([]string{"link"}, tc.args...)...); !reflect.DeepEqual(got, []any{tc.want}) {
			t.Errorf("link %v printed %v, want %v", tc.args, got, tc.want)
		}
	}
	// Each wording was given twice or names its own link: four links in all.
	links := ok[object](t, "show", b)["links"].(object)
	for key, want := range map[string][]any{
		"blocked_by": {a}, "related": {a}, "discovered_from": {a},
	} {
		if !reflect.DeepEqual(links[key], want) {
			t.Errorf("links.%s is %v, want %v", key, links[key], want)
		}
	}
	if parent := ok[object](t, "show", c)["parent"]; parent != p {
		t.Errorf("parent is %v, want %s", parent, p)
	}
}

func TestUnlinkTakesEitherWordingAndPrintsTheLinkRemoved(t *testing.T) {
	inInitialisedRepo(t)
	a, b := newID(t, "A"), newID(t, "B")
	ok[[]any](t, "link", a, "blocks", b)
	got := ok[[]any](t, "unlink", b, "blocked-by", a)
	if want := ([]any{object{"from": a, "type": "blocks", "to": b}}); !reflect.DeepEqual(got, want) {
		t.Errorf("unlink printed %v, want %v", got, want)
	}
	if blocks := ok[object](t, "show", a)["links"].(object)["blocks"]; len(blocks.([]any)) != 0 {
		t.Errorf("blocks is %v after unlink", blocks)
	}
}

func TestLinkAndUnlinkPlainOutputNamesTheCanonicalLink(t *testing.T) {
	inInitialisedRepo(t)
	a, b := newID(t, "A"), newID(t, "B")
	r := tess(t, "link", b, "blocked-by", a)
	if want := "linked " + a + " blocks " + b + "\n"; r.code != 0 || r.stdout != want {
		t.Errorf("link: exit %d, stdout %q, want %q (%s)", r.code, r.stdout, want, r.stderr)
	}
	r = tess(t, "unlink", a, "blocks", b)
	if want := "unlinked " + a + " blocks " + b + "\n"; r.code != 0 || r.stdout != want {
		t.Errorf("unlink: exit %d, stdout %q, want %q (%s)", r.code, r.stdout, want, r.stderr)
	}
}

func TestLinkRefusalsKeepTheirCodes(t *testing.T) {
	inInitialisedRepo(t)
	a, b, c := newID(t, "A"), newID(t, "B"), newID(t, "C")
	ok[[]any](t, "link", a, "parent-of", b)
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"link", a, "duplicates", b}, "invalid_argument"},
		{[]string{"link", a, "blocks"}, "invalid_argument"},
		{[]string{"link", "blocks", b}, "invalid_argument"},
		{[]string{"link", a, b}, "invalid_argument"},
		{[]string{"link", a, "blocks", b, "blocks", c}, "invalid_argument"},
		{[]string{"unlink", a}, "invalid_argument"},
		{[]string{"link", a, "blocks", "x-zzzzzzzz"}, "not_found"},
		{[]string{"link", c, "parent-of", b}, "has_parent"},
		{[]string{"link", b, "parent-of", a}, "cycle"},
		{[]string{"link", a, "blocks", a}, "cycle"},
		{[]string{"unlink", a, "blocks", c}, "not_found"},
		{[]string{"unlink", a, "supersedes", b}, "invalid_argument"},
		{[]string{"tree"}, "invalid_argument"},
		{[]string{"tree", "x-zzzzzzzz"}, "not_found"},
	} {
		wantError(t, tess(t, append(tc.args, "--json")...), tc.code)
	}
}

func TestTreePrintsNestedNodesAsJSON(t *testing.T) {
	inInitialisedRepo(t)
	root, child, blocker := newID(t, "Root"), newID(t, "Child"), newID(t, "Blocker")
	ok[[]any](t, "link", root, "parent-of", child)
	ok[[]any](t, "link", blocker, "blocks", child)
	got := ok[object](t, "tree", root)
	want := object{
		"id": root, "title": "Root", "status": "waiting", "blocked_by": []any{},
		"children": []any{object{
			"id": child, "title": "Child", "status": "waiting", "children": []any{},
			"blocked_by": []any{object{
				"id": blocker, "title": "Blocker", "status": "ready",
				"children": []any{}, "blocked_by": []any{},
			}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tree is %v, want %v", got, want)
	}
}

func TestTreePlainOutputIndentsChildrenAndMarksBlockers(t *testing.T) {
	inInitialisedRepo(t)
	root, child, blocker := newID(t, "Root"), newID(t, "Child"), newID(t, "Blocker")
	tess(t, "link", root, "parent-of", child)
	tess(t, "link", blocker, "blocks", child)
	r := tess(t, "tree", root)
	want := strings.Join([]string{
		"⊖ " + root + " Root",
		"  ⊖ " + child + " Child",
		"    blocked by ○ " + blocker + " Blocker",
		"",
	}, "\n")
	if r.code != 0 || r.stdout != want {
		t.Errorf("exit %d (%s), stdout:\n%s\nwant:\n%s", r.code, r.stderr, r.stdout, want)
	}
}

func TestLinkWithSeveralIssuesOnEitherSidePrintsEveryPairInOrder(t *testing.T) {
	inInitialisedRepo(t)
	a, b, c, d := newID(t, "A"), newID(t, "B"), newID(t, "C"), newID(t, "D")
	link := func(from, to string) any { return object{"from": from, "type": "blocks", "to": to} }

	got := ok[[]any](t, "link", a, b, "blocks", c, d)
	if want := []any{link(a, c), link(a, d), link(b, c), link(b, d)}; !reflect.DeepEqual(got, want) {
		t.Errorf("link printed %v, want %v", got, want)
	}
	// Said from the other end, the pairs follow the order typed.
	got = ok[[]any](t, "unlink", c, d, "blocked-by", a)
	if want := []any{link(a, c), link(a, d)}; !reflect.DeepEqual(got, want) {
		t.Errorf("unlink printed %v, want %v", got, want)
	}
	if blockedBy := ok[object](t, "show", d)["links"].(object)["blocked_by"]; !reflect.DeepEqual(blockedBy, []any{b}) {
		t.Errorf("d is blocked by %v, want only %s", blockedBy, b)
	}
}

func TestLinkAndUnlinkPlainOutputIsOneLinePerPair(t *testing.T) {
	inInitialisedRepo(t)
	a, b, c := newID(t, "A"), newID(t, "B"), newID(t, "C")
	r := tess(t, "link", a, b, "blocks", c)
	if want := "linked " + a + " blocks " + c + "\nlinked " + b + " blocks " + c + "\n"; r.code != 0 || r.stdout != want {
		t.Errorf("link: exit %d, stdout %q, want %q (%s)", r.code, r.stdout, want, r.stderr)
	}
	r = tess(t, "unlink", c, "blocked-by", a, b)
	if want := "unlinked " + a + " blocks " + c + "\nunlinked " + b + " blocks " + c + "\n"; r.code != 0 || r.stdout != want {
		t.Errorf("unlink: exit %d, stdout %q, want %q (%s)", r.code, r.stdout, want, r.stderr)
	}
}

func TestOneRefusedPairLinksNothing(t *testing.T) {
	inInitialisedRepo(t)
	a, b := newID(t, "A"), newID(t, "B")
	wantError(t, tess(t, "link", a, "blocks", b, "x-zzzzzzzz", "--json"), "not_found")
	if blocks := ok[object](t, "show", a)["links"].(object)["blocks"]; len(blocks.([]any)) != 0 {
		t.Errorf("blocks is %v after a refused link", blocks)
	}
}
