package git

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// ErrRemoteMoved is Push's error when the remote ref is not where the
// caller expected: someone else pushed first. Fetching and pushing again is
// the answer, so it is not a *tracker.Error; the caller decides when to
// stop trying.
var ErrRemoteMoved = errors.New("the remote ref moved")

// Fetch fetches one ref of the remote (a remote's name or a URL) into the
// local ref dst, overwriting it, and reports whether the remote has the
// ref. When it doesn't, nothing is fetched and dst is left as it was. A
// remote that can't be reached is a *tracker.Error with CodeOffline.
func (r Repo) Fetch(ctx context.Context, remote, ref, dst string) (bool, error) {
	_, err := r.run(ctx, nil, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules",
		"--end-of-options", remote, "+"+ref+":"+dst)
	if err == nil {
		return true, nil
	}
	var f *failure
	if !errors.As(err, &f) {
		return false, err
	}
	if strings.Contains(f.stderr, "couldn't find remote ref") {
		return false, nil
	}
	return false, offline(remote, f.stderr)
}

// Push pushes commit to the remote's ref, but only if the ref is still at
// expected (--force-with-lease); an empty expected means the remote must
// not have the ref yet. The errors: ErrRemoteMoved when the ref is
// somewhere else, CodePushRejected with git's reason when the remote
// refuses the update, CodeOffline when the remote can't be reached. The
// repository's pre-push hook does not run: this is not the user's push.
// LsRemote asks remote for the commit at ref without fetching anything:
// one process, no objects. found is false, with no error, when the remote
// is reachable but has no such ref; a remote that can't be reached is
// offline.
func (r Repo) LsRemote(ctx context.Context, remote, ref string) (id string, found bool, err error) {
	out, err := r.run(ctx, nil, "ls-remote", "--exit-code", "--end-of-options", remote, ref)
	if exitCode(err) == 2 {
		return "", false, nil
	}
	if err != nil {
		var f *failure
		if errors.As(err, &f) {
			return "", false, offline(remote, f.stderr)
		}
		return "", false, err
	}
	id, _, _ = strings.Cut(strings.TrimSpace(string(out)), "\t")
	return id, id != "", nil
}

func (r Repo) Push(ctx context.Context, remote, commit, ref, expected string) error {
	out, err := r.run(ctx, nil, "push", "--porcelain", "--no-verify", "--no-recurse-submodules", "--no-follow-tags",
		"--force-with-lease="+ref+":"+expected, "--end-of-options", remote, commit+":"+ref)
	if err == nil {
		return nil
	}
	var f *failure
	if !errors.As(err, &f) {
		return err
	}
	summary, rejected := rejection(string(out), ref)
	if !rejected {
		// Git never got as far as an answer about the ref.
		return offline(remote, f.stderr)
	}
	why := summary
	if open := strings.Index(summary, "("); open >= 0 && strings.HasSuffix(summary, ")") {
		why = summary[open+1 : len(summary)-1]
	}
	if moved(summary, why) {
		return fmt.Errorf("%w: %s", ErrRemoteMoved, why)
	}
	// What the remote's hooks printed is the rest of the reason.
	for _, line := range strings.Split(f.stderr, "\n") {
		if said, ok := strings.CutPrefix(line, "remote: "); ok {
			if said = strings.TrimSpace(said); said != "" {
				why += ": " + said
			}
		}
	}
	return &tracker.Error{
		Code:    CodePushRejected,
		Message: fmt.Sprintf("the remote %s refused the push: %s", remote, why),
		Hint:    "the remote's rules refused the update to " + ref + "; ask whoever runs the remote",
	}
}

// rejection finds the ref's line in push --porcelain's output,
// "<flag>\t<from>:<to>\t<summary> (<reason>)", and returns its summary if
// the flag says the ref was rejected.
func rejection(porcelain, ref string) (string, bool) {
	for _, line := range strings.Split(porcelain, "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) == 3 && fields[0] == "!" && strings.HasSuffix(fields[1], ":"+ref) {
			return strings.TrimSpace(fields[2]), true
		}
	}
	return "", false
}

// moved reports whether a rejection means the remote ref was not where we
// thought. Git's own check says so before sending ("stale info" under
// --force-with-lease, "fetch first" and "non-fast-forward" otherwise); the
// remote says so when another push lands between its advertisement and our
// update, and its lock on the ref fails.
func moved(summary, why string) bool {
	if strings.HasPrefix(summary, "[rejected]") {
		switch why {
		case "stale info", "fetch first", "non-fast-forward":
			return true
		}
		return false
	}
	return strings.HasPrefix(why, "failed to update ref") || strings.HasPrefix(why, "failed to lock")
}

func offline(remote, stderr string) error {
	return &tracker.Error{
		Code:    CodeOffline,
		Message: fmt.Sprintf("can't reach the remote %s: %s", remote, reason(stderr)),
		Hint:    "check the connection and the remote's URL (`git remote -v`), then run `tess sync` again",
	}
}
