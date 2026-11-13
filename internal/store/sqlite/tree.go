package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// TreeNode is one issue in a dependency tree: its children and the issues it
// is blocked by, each a node of the same shape. Status is the displayed
// status. Siblings are in list order (priority, created_at, id). An issue
// that is reached by two paths appears under both.
type TreeNode struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Status    string     `json:"status"`
	Children  []TreeNode `json:"children"`
	BlockedBy []TreeNode `json:"blocked_by"`
}

// treeDepth is how far down a tree is followed. Cycles can't be created
// (ADR-0016), so this only keeps a damaged database from recursing forever.
const treeDepth = 100

// Tree returns the dependency tree below an issue: what it waits on, through
// its children and its blockers, all the way down. It follows
// readiness_edges, so retired issues are left out; a retired root stands
// alone. ok is false when there is no such issue.
func Tree(ctx context.Context, tx *sql.Tx, rootID string) (root TreeNode, ok bool, err error) {
	// The issues the tree reaches, in list order, then the edges between them.
	const reach = `WITH RECURSIVE reach (id) AS (
	     SELECT ?1
	     UNION
	     SELECT CASE e.type WHEN 'parent' THEN e.to_id ELSE e.from_id END
	     FROM reach
	     JOIN readiness_edges e ON (e.type = 'parent' AND e.from_id = reach.id)
	                            OR (e.type = 'blocks' AND e.to_id = reach.id)
	 ) `
	type edges struct{ children, blockers []string }
	nodes := map[string]*TreeNode{}
	under := map[string]*edges{}

	rows, err := tx.QueryContext(ctx, reach+
		`SELECT i.id, i.title, coalesce(s.display_status, i.status)
		 FROM reach
		 JOIN issues i ON i.id = reach.id
		 LEFT JOIN issue_states s ON s.id = i.id
		 ORDER BY i.priority, i.created_at, i.id`, rootID)
	if err != nil {
		return TreeNode{}, false, fmt.Errorf("read the tree of %s: %w", rootID, err)
	}
	defer rows.Close()
	for rows.Next() {
		n := &TreeNode{}
		if err := rows.Scan(&n.ID, &n.Title, &n.Status); err != nil {
			return TreeNode{}, false, err
		}
		nodes[n.ID] = n
		under[n.ID] = &edges{}
	}
	if err := rows.Err(); err != nil {
		return TreeNode{}, false, err
	}
	if nodes[rootID] == nil {
		return TreeNode{}, false, nil
	}

	// Ordered by the far end's place in list order, so each node's children
	// and blockers come out already sorted.
	links, err := tx.QueryContext(ctx, reach+
		`SELECT e.type, e.from_id, e.to_id
		 FROM readiness_edges e
		 JOIN issues far ON far.id = CASE e.type WHEN 'parent' THEN e.to_id ELSE e.from_id END
		 WHERE CASE e.type WHEN 'parent' THEN e.from_id ELSE e.to_id END IN (SELECT id FROM reach)
		 ORDER BY far.priority, far.created_at, far.id`, rootID)
	if err != nil {
		return TreeNode{}, false, fmt.Errorf("read the tree of %s: %w", rootID, err)
	}
	defer links.Close()
	for links.Next() {
		var typ, from, to string
		if err := links.Scan(&typ, &from, &to); err != nil {
			return TreeNode{}, false, err
		}
		if typ == LinkParent {
			under[from].children = append(under[from].children, to)
		} else {
			under[to].blockers = append(under[to].blockers, from)
		}
	}
	if err := links.Err(); err != nil {
		return TreeNode{}, false, err
	}

	var build func(id string, depth int) TreeNode
	build = func(id string, depth int) TreeNode {
		n := *nodes[id]
		n.Children, n.BlockedBy = []TreeNode{}, []TreeNode{}
		if depth >= treeDepth {
			return n
		}
		for _, child := range under[id].children {
			n.Children = append(n.Children, build(child, depth+1))
		}
		for _, blocker := range under[id].blockers {
			n.BlockedBy = append(n.BlockedBy, build(blocker, depth+1))
		}
		return n
	}
	return build(rootID, 0), true, nil
}
