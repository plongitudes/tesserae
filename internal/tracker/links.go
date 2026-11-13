package tracker

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/plongitudes/tesserae/internal/store/sqlite"
)

// Link is a link in its canonical direction (blocks: blocker → blocked;
// parent: parent → child; discovered-from: discovered → origin).
type Link = sqlite.Link

// TreeNode is one issue in a dependency tree, with the nodes of its children
// and of the issues it is blocked by.
type TreeNode = sqlite.TreeNode

// LinkWordings are the words a link can be given in, as in `tess link A
// WORDING B`. Each type that has a direction can be said from either end.
var LinkWordings = []string{"blocks", "blocked-by", "parent-of", "child-of", "related", "discovered-from"}

// canonical turns `a WORDING b` into the link in its canonical direction.
// "parent", the type's own name, reads as parent-of: it is what a link is
// printed as, so a printed link can be given back.
func canonical(a, wording, b string) (Link, error) {
	switch wording {
	case "blocks":
		return Link{From: a, Type: sqlite.LinkBlocks, To: b}, nil
	case "blocked-by":
		return Link{From: b, Type: sqlite.LinkBlocks, To: a}, nil
	case "parent-of", "parent":
		return Link{From: a, Type: sqlite.LinkParent, To: b}, nil
	case "child-of":
		return Link{From: b, Type: sqlite.LinkParent, To: a}, nil
	case "related":
		return Link{From: a, Type: sqlite.LinkRelated, To: b}, nil
	case "discovered-from":
		return Link{From: a, Type: sqlite.LinkDiscoveredFrom, To: b}, nil
	}
	return Link{}, invalid(fmt.Sprintf("unknown link type %q", wording), "link types: "+strings.Join(LinkWordings, ", "))
}

func (t *tracker) Link(ctx context.Context, a, wording, b string) (Link, error) {
	if _, err := canonical(a, wording, b); err != nil {
		return Link{}, err
	}
	var link Link
	err := t.store.Write(ctx, func(tx *sql.Tx) error {
		idA, err := resolveLive(ctx, tx, a)
		if err != nil {
			return err
		}
		idB, err := resolveLive(ctx, tx, b)
		if err != nil {
			return err
		}
		link, _ = canonical(idA, wording, idB)
		readiness := link.Type == sqlite.LinkBlocks || link.Type == sqlite.LinkParent
		if idA == idB {
			if readiness {
				return cycleError(link)
			}
			return invalid(fmt.Sprintf("%s can't be linked to itself", idA), "")
		}
		existing, ok, err := sqlite.FindLink(ctx, tx, link)
		if err != nil {
			return err
		}
		if ok {
			link = existing
			return nil
		}
		if link.Type == sqlite.LinkParent {
			// Read from the links, not the issue: a retired parent doesn't
			// show, but its link is there and comes back on restore.
			parent, has, err := sqlite.ParentOf(ctx, tx, link.To)
			if err != nil {
				return err
			}
			if has {
				return &Error{
					Code:    CodeHasParent,
					Message: fmt.Sprintf("%s already has a parent, %s", link.To, parent),
					Hint:    fmt.Sprintf("tess unlink %s parent-of %s first", parent, link.To),
				}
			}
		}
		if readiness {
			loop, err := sqlite.ClosesLoop(ctx, tx, link.From, link.To)
			if err != nil {
				return err
			}
			if loop {
				return cycleError(link)
			}
		}
		return sqlite.InsertLink(ctx, tx, link, t.now(), t.actor)
	})
	if err != nil {
		return Link{}, err
	}
	return link, nil
}

func (t *tracker) Unlink(ctx context.Context, a, wording, b string) (Link, error) {
	if _, err := canonical(a, wording, b); err != nil {
		return Link{}, err
	}
	var link Link
	err := t.store.Write(ctx, func(tx *sql.Tx) error {
		// A retired issue is reached by its exact ID, so that a link to one
		// can be taken off; which end may be retired is decided below.
		idA, _, err := resolve(ctx, tx, a)
		if err != nil {
			return err
		}
		idB, _, err := resolve(ctx, tx, b)
		if err != nil {
			return err
		}
		asked, _ := canonical(idA, wording, idB)
		stored, ok, err := sqlite.FindLink(ctx, tx, asked)
		if err != nil {
			return err
		}
		if !ok {
			return &Error{
				Code:    CodeNotFound,
				Message: fmt.Sprintf("there is no %s link from %s to %s", asked.Type, asked.From, asked.To),
				Hint:    fmt.Sprintf("tess show %s lists its links", idA),
			}
		}
		// Removing a link changes the issue it is logged on (the dependent,
		// the child), and a retired issue can't be changed. The other end
		// may be retired.
		if _, err := resolveLive(ctx, tx, sqlite.LinkOwner(stored)); err != nil {
			return err
		}
		link = stored
		return sqlite.DeleteLink(ctx, tx, stored, t.now(), t.actor)
	})
	if err != nil {
		return Link{}, err
	}
	return link, nil
}

func cycleError(l Link) error {
	e := &Error{Code: CodeCycle, Hint: fmt.Sprintf("tess tree %s shows what it depends on", l.From)}
	switch {
	case l.From == l.To && l.Type == sqlite.LinkBlocks:
		e.Message = fmt.Sprintf("%s can't block itself", l.From)
		e.Hint = ""
	case l.From == l.To:
		e.Message = fmt.Sprintf("%s can't be its own parent", l.From)
		e.Hint = ""
	case l.Type == sqlite.LinkBlocks:
		e.Message = fmt.Sprintf("%s blocking %s would close a loop: %s already depends on %s", l.From, l.To, l.From, l.To)
	default:
		e.Message = fmt.Sprintf("%s as the parent of %s would close a loop: %s already depends on %s", l.From, l.To, l.From, l.To)
	}
	return e
}

func (t *tracker) Tree(ctx context.Context, partialID string) (TreeNode, error) {
	var root TreeNode
	err := t.store.Read(ctx, func(tx *sql.Tx) error {
		id, _, err := resolve(ctx, tx, partialID)
		if err != nil {
			return err
		}
		node, ok, err := sqlite.Tree(ctx, tx, id)
		if err == nil && !ok {
			err = fmt.Errorf("issue %s is missing", id)
		}
		root = node
		return err
	})
	if err != nil {
		return TreeNode{}, err
	}
	return root, nil
}
