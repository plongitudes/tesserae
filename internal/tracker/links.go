package tracker

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
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

// IsLinkWording reports whether a word is a link type as link and unlink take
// it: one of LinkWordings, or "parent".
func IsLinkWording(word string) bool {
	_, err := canonical("", word, "")
	return err == nil
}

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

func (t *tracker) Link(ctx context.Context, left []string, wording string, right []string) ([]Link, error) {
	return t.eachPair(ctx, left, wording, right, resolveLive, func(tx *sql.Tx, asked Link) (Link, error) {
		existing, ok, err := admitLink(ctx, tx, asked)
		if err != nil || ok {
			return existing, err
		}
		return asked, sqlite.InsertLink(ctx, tx, asked, t.now(), t.actor)
	})
}

// eachPair runs change, in one write transaction, on the link `a WORDING b`
// for every pair of an issue on the left and one on the right: the first
// left issue with each right one in turn, then the next. Each pair is judged
// with the ones before it already made. One refusal undoes them all. An
// issue named twice on one side counts once.
func (t *tracker) eachPair(ctx context.Context, left []string, wording string, right []string,
	find func(context.Context, *sql.Tx, string) (string, error),
	change func(tx *sql.Tx, asked Link) (Link, error),
) ([]Link, error) {
	if _, err := canonical("", wording, ""); err != nil {
		return nil, err
	}
	if len(left) == 0 || len(right) == 0 {
		return nil, invalid("a link needs an issue on each side of the link type", "as in `tess link A "+wording+" B`")
	}
	var links []Link
	err := t.store.Write(ctx, func(tx *sql.Tx) error {
		side := func(partialIDs []string) ([]string, error) {
			var ids []string
			for _, partial := range partialIDs {
				id, err := find(ctx, tx, partial)
				if err != nil {
					return nil, err
				}
				if !slices.Contains(ids, id) {
					ids = append(ids, id)
				}
			}
			return ids, nil
		}
		lefts, err := side(left)
		if err != nil {
			return err
		}
		rights, err := side(right)
		if err != nil {
			return err
		}
		for _, a := range lefts {
			for _, b := range rights {
				asked, _ := canonical(a, wording, b)
				link, err := change(tx, asked)
				if err != nil {
					return err
				}
				links = append(links, link)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return links, nil
}

// admitLink holds a link between two issues that exist, given in its
// canonical direction, to the rules for a new link, whoever is making it
// (Link, Import). A link that is already there, a related one either way
// round, is returned as it is stored, and there is nothing to add. Otherwise
// it is refused when it links an issue to itself, gives an issue a second
// parent (has_parent), leaves a blocks link between an issue and its own
// ancestor, or closes a loop of blocks and parent links (both cycle).
func admitLink(ctx context.Context, tx *sql.Tx, link Link) (existing Link, ok bool, err error) {
	if link.From == link.To {
		if link.AffectsReadiness() {
			return Link{}, false, cycleError(link)
		}
		return Link{}, false, invalid(fmt.Sprintf("%s can't be linked to itself", link.From), "")
	}
	if existing, ok, err = sqlite.FindLink(ctx, tx, link); err != nil || ok {
		return existing, ok, err
	}
	if link.Type == sqlite.LinkParent {
		// Read from the links, not the issue: a retired parent doesn't
		// show, but its link is there and comes back on restore.
		parent, has, err := sqlite.ParentOf(ctx, tx, link.To)
		if err != nil {
			return Link{}, false, err
		}
		if has {
			return Link{}, false, &Error{
				Code:    CodeHasParent,
				Message: fmt.Sprintf("%s already has a parent, %s", link.To, parent),
				Hint:    fmt.Sprintf("tess unlink %s parent-of %s first", parent, link.To),
			}
		}
	}
	if link.AffectsReadiness() {
		if err := checkHierarchy(ctx, tx, link); err != nil {
			return Link{}, false, err
		}
		loop, err := sqlite.ClosesLoop(ctx, tx, link.From, link.To)
		if err != nil {
			return Link{}, false, err
		}
		if loop {
			return Link{}, false, cycleError(link)
		}
	}
	return Link{}, false, nil
}

func (t *tracker) Unlink(ctx context.Context, left []string, wording string, right []string) ([]Link, error) {
	// A retired issue is reached by its exact ID, so that a link to one can
	// be taken off; which end may be retired is decided below.
	find := func(ctx context.Context, tx *sql.Tx, partialID string) (string, error) {
		id, _, err := resolve(ctx, tx, partialID)
		return id, err
	}
	return t.eachPair(ctx, left, wording, right, find, func(tx *sql.Tx, asked Link) (Link, error) {
		stored, ok, err := sqlite.FindLink(ctx, tx, asked)
		if err != nil {
			return Link{}, err
		}
		if !ok {
			return Link{}, &Error{
				Code:    CodeNotFound,
				Message: fmt.Sprintf("there is no %s link from %s to %s", asked.Type, asked.From, asked.To),
				Hint:    fmt.Sprintf("tess show %s lists its links", asked.From),
			}
		}
		// Removing a link changes the issue it is logged on (the dependent,
		// the child), and a retired issue can't be changed. The other end
		// may be retired.
		if _, err := resolveLive(ctx, tx, sqlite.LinkOwner(stored)); err != nil {
			return Link{}, err
		}
		return stored, sqlite.DeleteLink(ctx, tx, stored, t.now(), t.actor)
	})
}

// checkHierarchy refuses a link that would leave a blocks link between an
// issue and its own ancestor (ADR-0016): the blocks link itself, or the
// parent link that puts the two ends of one in the same line. Either way
// round it is a deadlock, though only one way is a loop in the links.
func checkHierarchy(ctx context.Context, tx *sql.Tx, link Link) error {
	if link.Type == sqlite.LinkBlocks {
		// The blocker is below the issue it would block.
		depth, err := sqlite.AncestorDepth(ctx, tx, link.To, link.From)
		if err != nil {
			return err
		}
		if depth > 0 {
			kin := "a child"
			if depth > 1 {
				kin = "a descendant"
			}
			return hierarchyError(link.To, fmt.Sprintf(
				"%s is %s of %s, and a parent's open children already hold it; %s blocking %s would make a dependency loop",
				link.From, kin, link.To, link.From, link.To))
		}
		// The blocker is above it.
		if depth, err = sqlite.AncestorDepth(ctx, tx, link.From, link.To); err != nil {
			return err
		}
		if depth > 0 {
			return hierarchyError(link.From, fmt.Sprintf(
				"%s is an ancestor of %s, so %s could never start; %s blocking %s would make a dependency loop",
				link.From, link.To, link.To, link.From, link.To))
		}
		return nil
	}

	// A parent link. One that would put an issue under its own descendant is
	// a loop of parent links, and is refused as one.
	if depth, err := sqlite.AncestorDepth(ctx, tx, link.To, link.From); err != nil || depth > 0 {
		return err
	}
	blocks, ok, err := sqlite.BlocksLinkInLine(ctx, tx, link.From, link.To)
	if err != nil || !ok {
		return err
	}
	// The blocker is the new child or below it, or the new parent or above.
	below, err := isOrIsUnder(ctx, tx, blocks.From, link.To)
	if err != nil {
		return err
	}
	if below {
		return hierarchyError(link.From, fmt.Sprintf(
			"%s as the parent of %s would make %s a descendant of %s, which it blocks, and a parent's open children already hold it; that would make a dependency loop",
			link.From, link.To, blocks.From, blocks.To))
	}
	return hierarchyError(link.From, fmt.Sprintf(
		"%s as the parent of %s would make %s an ancestor of %s, which it blocks, so %s could never start; that would make a dependency loop",
		link.From, link.To, blocks.From, blocks.To, blocks.To))
}

func isOrIsUnder(ctx context.Context, tx *sql.Tx, id, top string) (bool, error) {
	if id == top {
		return true, nil
	}
	depth, err := sqlite.AncestorDepth(ctx, tx, top, id)
	return depth > 0, err
}

// hierarchyError is a cycle refusal that points at the tree under top, the
// upper issue of the two.
func hierarchyError(top, message string) error {
	return &Error{Code: CodeCycle, Message: message, Hint: fmt.Sprintf("tess show %s --tree shows what is under it", top)}
}

func cycleError(l Link) error {
	e := &Error{Code: CodeCycle, Hint: fmt.Sprintf("tess show %s --tree shows what it depends on", l.From)}
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
