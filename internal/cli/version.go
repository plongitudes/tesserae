package cli

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// readBuildInfo is debug.ReadBuildInfo; a test substitutes a binary of its
// own making.
var readBuildInfo = debug.ReadBuildInfo

// build is what `tess version` says about this binary. Go records it in the
// binary itself: the module version when installed with `go install
// ...@VERSION`, and for a build of a checkout a pseudo-version made from the
// commit, the commit itself, and whether the checkout had uncommitted changes.
type build struct {
	Version  string `json:"version"`            // v0.1.0; a pseudo-version, or devel, for a build of a checkout
	Revision string `json:"revision,omitempty"` // the commit, when built from a checkout
	Modified bool   `json:"modified"`           // the checkout had uncommitted changes
	Go       string `json:"go"`                 // the toolchain that built it
}

// describe reads the build out of info. A version of "(devel)" is what Go
// before 1.24 writes for a build of a checkout; it is said as devel.
func describe(info *debug.BuildInfo, ok bool) build {
	b := build{Version: "unknown", Go: "unknown"}
	if !ok {
		return b
	}
	b.Go = info.GoVersion
	if v := info.Main.Version; v != "" && v != "(devel)" {
		b.Version = v
	} else {
		b.Version = "devel"
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Revision = s.Value
		case "vcs.modified":
			b.Modified = s.Value == "true"
		}
	}
	return b
}

// line is the plain form: `tess v0.1.0 (go1.27.3)`. A build of a checkout
// says its commit and whether the checkout was modified, unless the version
// says so itself: since Go 1.24 a build between tags is a pseudo-version,
// `v0.1.1-0.20260101120000-0123456789ab+dirty`, that carries both.
func (b build) line() string {
	var facts []string
	if rev := shortRevision(b.Revision); rev != "" && !strings.Contains(b.Version, rev) {
		facts = append(facts, rev)
	}
	if b.Modified && !strings.HasSuffix(b.Version, "+dirty") {
		facts = append(facts, "modified")
	}
	facts = append(facts, b.Go)
	return fmt.Sprintf("tess %s (%s)\n", b.Version, strings.Join(facts, ", "))
}

// shortRevision is the first twelve characters of a commit, as git's own
// short form would be for a repository of this size.
func shortRevision(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

func (a *app) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Say which build of tess this is",
		Long: `Say which build of tess this is.

The version is the one tess was installed at (go install ...@v0.1.0). A build
of a checkout names the commit it was built from, and says so when the
checkout had uncommitted changes. Put the line in a bug report.`,
		Example: `  tess version
  tess version --json`,
		Args: cobra.NoArgs,
		RunE: action(func(cmd *cobra.Command, args []string) error {
			b := describe(readBuildInfo())
			return a.print(b, func(w io.Writer) { fmt.Fprint(w, b.line()) })
		}),
	}
}
