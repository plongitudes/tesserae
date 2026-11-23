package git

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Signature is who made a commit and when. Git reads none of it from the
// environment or its config: a commit is what its arguments say.
type Signature struct {
	Name, Email string
	When        time.Time
}

// Commit is a commit to write. Its tree is the first parent's tree (empty
// with no parents) with Delete removed and then Files written, so only what
// changed since the first parent needs sending.
type Commit struct {
	Parents           []string
	Author, Committer Signature
	Message           string
	Files             map[string][]byte // path -> the blob's bytes
	Delete            []string          // paths to drop from the first parent's tree
}

// scratchBranch is the name fast-import's stream needs for the commit it
// builds. The stream resets it before the end, so no ref is ever written.
const scratchBranch = "refs/fast-import/scratch"

// BlobHash is the object ID git gives a blob with these bytes, computed
// here so a tree comparison needs no git process (SHA-1 repositories).
func BlobHash(data []byte) string { return HashBlob("sha1", data) }

// HashBlob is BlobHash in the repository's object format, "sha1" or
// "sha256" (ObjectFormat): the same recipe, a different hash function. An
// unknown format is taken as sha1.
func HashBlob(format string, data []byte) string {
	var h hash.Hash = sha1.New()
	if format == "sha256" {
		h = sha256.New()
	}
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// ObjectFormat is the repository's hash algorithm for objects: "sha1", or
// "sha256" for a repository made with --object-format=sha256.
func (r Repo) ObjectFormat(ctx context.Context) (string, error) {
	out, err := r.run(ctx, nil, "rev-parse", "--show-object-format")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// WriteCommit writes the commit, its trees and its blobs in one
// fast-import process and returns the commit's ID. No ref moves.
func (r Repo) WriteCommit(ctx context.Context, c Commit) (string, error) {
	var stream bytes.Buffer
	data := func(b []byte) {
		fmt.Fprintf(&stream, "data %d\n", len(b))
		stream.Write(b)
		stream.WriteByte('\n')
	}
	fmt.Fprintf(&stream, "commit %s\nmark :1\n", scratchBranch)
	fmt.Fprintf(&stream, "author %s\ncommitter %s\n", ident(c.Author), ident(c.Committer))
	data([]byte(c.Message))
	for i, parent := range c.Parents {
		if !isHash(parent) {
			return "", fmt.Errorf("git: parent %q is not a commit ID", parent)
		}
		if i == 0 {
			fmt.Fprintf(&stream, "from %s\n", parent)
		} else {
			fmt.Fprintf(&stream, "merge %s\n", parent)
		}
	}
	deleted := append([]string(nil), c.Delete...)
	sort.Strings(deleted)
	for _, path := range deleted {
		quoted, err := quotePath(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&stream, "D %s\n", quoted)
	}
	paths := make([]string, 0, len(c.Files))
	for path := range c.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		quoted, err := quotePath(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&stream, "M 100644 inline %s\n", quoted)
		data(c.Files[path])
	}
	// Resetting the branch with no commit leaves fast-import nothing to
	// write to a ref; the commit is reached through its mark.
	fmt.Fprintf(&stream, "\nreset %s\n\nget-mark :1\ndone\n", scratchBranch)

	out, err := r.run(ctx, &stream, "fast-import", "--quiet", "--done", "--date-format=raw")
	if err != nil {
		return "", err
	}
	hash := strings.TrimSpace(string(out))
	if !isHash(hash) {
		return "", fmt.Errorf("git fast-import: expected the commit's ID, got %q", hash)
	}
	return hash, nil
}

// ident is a signature as fast-import reads it, in UTC. The characters git
// can't hold in a name or an email are dropped, as git itself drops them.
func ident(s Signature) string {
	clean := strings.NewReplacer("<", "", ">", "", "\n", "", "\x00", "")
	name := strings.TrimSpace(clean.Replace(s.Name))
	if name != "" {
		name += " "
	}
	return fmt.Sprintf("%s<%s> %d +0000", name, strings.TrimSpace(clean.Replace(s.Email)), s.When.Unix())
}

// quotePath checks a tree path and writes it as fast-import reads it.
func quotePath(path string) (string, error) {
	if strings.ContainsAny(path, "\n\x00") {
		return "", fmt.Errorf("git: path %q has a character a tree path can't hold", path)
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("git: %q is not a path inside a tree", path)
		}
	}
	if strings.HasPrefix(path, `"`) {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(path) + `"`, nil
	}
	return path, nil
}

// isHash reports whether s is a full object ID.
func isHash(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ListTree returns every file in a commit's tree, path to blob ID, in one
// ls-tree process.
func (r Repo) ListTree(ctx context.Context, commit string) (map[string]string, error) {
	if !isHash(commit) {
		return nil, fmt.Errorf("git: %q is not a commit ID", commit)
	}
	out, err := r.run(ctx, nil, "ls-tree", "-r", "-z", commit)
	if err != nil {
		return nil, err
	}
	tree := map[string]string{}
	// Each entry is "<mode> <type> <id>\t<path>" and a NUL.
	for _, entry := range strings.Split(string(out), "\x00") {
		if entry == "" {
			continue
		}
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("git ls-tree: can't read the entry %q", entry)
		}
		if fields[1] != "blob" {
			continue
		}
		tree[path] = fields[2]
	}
	return tree, nil
}

// ReadBlobs returns the bytes of each blob, in the order asked, from one
// cat-file process.
func (r Repo) ReadBlobs(ctx context.Context, hashes []string) ([][]byte, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	var request bytes.Buffer
	for _, hash := range hashes {
		if !isHash(hash) {
			return nil, fmt.Errorf("git: %q is not a blob ID", hash)
		}
		request.WriteString(hash)
		request.WriteByte('\n')
	}
	out, err := r.run(ctx, &request, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	blobs := make([][]byte, 0, len(hashes))
	// Each answer is "<id> <type> <size>\n", the bytes, and a newline; or
	// "<id> missing\n".
	for _, hash := range hashes {
		header, rest, ok := bytes.Cut(out, []byte("\n"))
		if !ok {
			return nil, fmt.Errorf("git cat-file: the answer stops before %s", hash)
		}
		fields := strings.Fields(string(header))
		if len(fields) != 3 || fields[1] != "blob" {
			return nil, fmt.Errorf("git cat-file: wanted the blob %s, got %q", hash, header)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size < 0 || size+1 > len(rest) {
			return nil, fmt.Errorf("git cat-file: can't read the blob %s (%q)", hash, header)
		}
		blobs = append(blobs, rest[:size:size])
		out = rest[size+1:]
	}
	return blobs, nil
}
