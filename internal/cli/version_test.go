package cli

import (
	"runtime/debug"
	"strings"
	"testing"
)

// built makes `tess version` read a binary described by info for the test.
func built(t *testing.T, info *debug.BuildInfo, ok bool) {
	t.Helper()
	was := readBuildInfo
	t.Cleanup(func() { readBuildInfo = was })
	readBuildInfo = func() (*debug.BuildInfo, bool) { return info, ok }
}

func TestVersionOfAnInstalledReleaseIsTheModuleVersion(t *testing.T) {
	isolate(t)
	built(t, &debug.BuildInfo{GoVersion: "go1.27.3", Main: debug.Module{Version: "v0.1.0"}}, true)
	wantPlain(t, tess(t, "version"), "tess v0.1.0 (go1.27.3)")
	got := ok[object](t, "version")
	if got["version"] != "v0.1.0" || got["go"] != "go1.27.3" || got["modified"] != false {
		t.Errorf("json is %v", got)
	}
	if _, has := got["revision"]; has {
		t.Errorf("json names a revision an installed release does not have: %v", got)
	}
}

func TestVersionOfACheckoutBuildIsDevelWithTheCommit(t *testing.T) {
	isolate(t)
	built(t, &debug.BuildInfo{
		GoVersion: "go1.27.3",
		Main:      debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
			{Key: "vcs.modified", Value: "true"},
		},
	}, true)
	wantPlain(t, tess(t, "version"), "tess devel (0123456789ab, modified, go1.27.3)")
	got := ok[object](t, "version")
	if got["version"] != "devel" || got["revision"] != "0123456789abcdef0123456789abcdef01234567" || got["modified"] != true {
		t.Errorf("json is %v", got)
	}
}

func TestVersionOfABuildBetweenTagsIsThePseudoVersionAlone(t *testing.T) {
	isolate(t)
	built(t, &debug.BuildInfo{
		GoVersion: "go1.27.3",
		Main:      debug.Module{Version: "v0.1.1-0.20260101120000-0123456789ab+dirty"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
			{Key: "vcs.modified", Value: "true"},
		},
	}, true)
	// The pseudo-version already names the commit and says +dirty.
	wantPlain(t, tess(t, "version"), "tess v0.1.1-0.20260101120000-0123456789ab+dirty (go1.27.3)")
}

func TestVersionWithoutBuildInformationSaysUnknown(t *testing.T) {
	isolate(t)
	built(t, nil, false)
	wantPlain(t, tess(t, "version"), "tess unknown (unknown)")
}

func TestVersionNeedsNoRepositoryAndIsNotInTheOrientation(t *testing.T) {
	inDir(t, isolate(t), "nowhere")
	if r := tess(t, "version"); r.code != 0 || !strings.HasPrefix(r.stdout, "tess ") {
		t.Errorf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if r := tess(t); strings.Contains(r.stdout, "version") {
		t.Errorf("orientation mentions version:\n%s", r.stdout)
	}
}
