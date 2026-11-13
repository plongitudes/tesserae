package tracker_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/plongitudes/tesserae/internal/tracker"
)

func (f *fixture) tree(id string) tracker.TreeNode {
	f.t.Helper()
	n, err := f.tr.Tree(f.ctx, id)
	if err != nil {
		f.t.Fatalf("Tree(%q): %v", id, err)
	}
	return n
}

// leaf is a tree node with nothing under it.
func leaf(id, title, status string) tracker.TreeNode {
	return tracker.TreeNode{ID: id, Title: title, Status: status, Children: []tracker.TreeNode{}, BlockedBy: []tracker.TreeNode{}}
}

func wantTree(t *testing.T, got, want tracker.TreeNode) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", "  ")
		w, _ := json.MarshalIndent(want, "", "  ")
		t.Errorf("tree:\n got  %s\n want %s", g, w)
	}
}

func TestTreeOfAnIssueOnItsOwn(t *testing.T) {
	f := newFixture(t)
	a := f.titled("Alone")
	got := f.tree(a[2:]) // a partial ID resolves
	wantTree(t, got, leaf(a, "Alone", "ready"))

	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"id":"` + a + `","title":"Alone","status":"ready","children":[],"blocked_by":[]}`; string(b) != want {
		t.Errorf("JSON = %s\n   want %s", b, want)
	}
}

func TestTreeNestsChildrenAndBlockers(t *testing.T) {
	f := newFixture(t)
	root := f.titled("Root")
	f.tick()
	kid := f.create(tracker.NewIssue{Title: "Kid", Parent: root}).ID
	f.tick()
	grandkid := f.create(tracker.NewIssue{Title: "Grandkid", Parent: kid}).ID
	f.tick()
	blocker := f.titled("Blocker")
	f.tick()
	deep := f.titled("Blocker's blocker")
	f.link(blocker, "blocks", root)
	f.link(deep, "blocks", blocker)
	f.link(deep, "blocks", grandkid)
	f.setStatus(deep, "in-progress")

	deepNode := leaf(deep, "Blocker's blocker", "in-progress")
	grandkidNode := leaf(grandkid, "Grandkid", "waiting")
	grandkidNode.BlockedBy = []tracker.TreeNode{deepNode}
	kidNode := leaf(kid, "Kid", "waiting")
	kidNode.Children = []tracker.TreeNode{grandkidNode}
	blockerNode := leaf(blocker, "Blocker", "waiting")
	blockerNode.BlockedBy = []tracker.TreeNode{deepNode}
	want := leaf(root, "Root", "waiting")
	want.Children = []tracker.TreeNode{kidNode}
	want.BlockedBy = []tracker.TreeNode{blockerNode}
	wantTree(t, f.tree(root), want)

	// The tree runs down from the issue asked for: not up to its parent, nor
	// out to what it blocks.
	wantTree(t, f.tree(kid), kidNode)
	wantTree(t, f.tree(deep), deepNode)
}

func TestTreeListsSiblingsInListOrder(t *testing.T) {
	f := newFixture(t)
	root := f.titled("Root")
	f.tick()
	late := f.create(tracker.NewIssue{Title: "Late", Parent: root}).ID
	f.tick()
	urgent := f.create(tracker.NewIssue{Title: "Urgent", Parent: root, Priority: ptr(0)}).ID
	f.tick()
	b2 := f.titled("Second blocker")
	f.tick()
	b1 := f.create(tracker.NewIssue{Title: "First blocker", Priority: ptr(1)}).ID
	f.link(b2, "blocks", root)
	f.link(b1, "blocks", root)

	got := f.tree(root)
	ids := func(nodes []tracker.TreeNode) []string {
		out := []string{}
		for _, n := range nodes {
			out = append(out, n.ID)
		}
		return out
	}
	wantStrings(t, "children", ids(got.Children), urgent, late)
	wantStrings(t, "blocked_by", ids(got.BlockedBy), b1, b2)
}

func TestTreeShowsFinishedIssuesAndLeavesOutRetiredOnes(t *testing.T) {
	f := newFixture(t)
	root := f.titled("Root")
	f.tick()
	done := f.create(tracker.NewIssue{Title: "Done child", Parent: root}).ID
	f.tick()
	gone := f.create(tracker.NewIssue{Title: "Retired child", Parent: root}).ID
	f.tick()
	goneBlocker := f.titled("Retired blocker")
	f.link(goneBlocker, "blocks", root)
	f.retire(gone)
	f.retire(goneBlocker)
	f.setStatus(done, "done")

	want := leaf(root, "Root", "ready")
	want.Children = []tracker.TreeNode{leaf(done, "Done child", "done")}
	wantTree(t, f.tree(root), want)

	// A retired issue is reached by its exact ID and stands alone.
	wantTree(t, f.tree(gone), leaf(gone, "Retired child", "ready"))
}

func TestTreeRefusesAnUnknownID(t *testing.T) {
	f := newFixture(t)
	f.titled("A")
	_, err := f.tr.Tree(f.ctx, "t-zzzzzzzz")
	wantCode(t, err, tracker.CodeNotFound)
}
