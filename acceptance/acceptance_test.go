// Package acceptance is the locked loop-1 acceptance suite. See README.md.
// The build loop must not edit anything in this directory.
package acceptance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/plongitudes/tesserae/internal/cli"
	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"tess": func() { os.Exit(cli.Main()) },
	})
}

func TestAcceptance(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir:                 "testdata",
		RequireExplicitExec: true,
		Setup:               setup,
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"capture": cmdCapture,
			"getjson": cmdGetJSON,
			"json":    cmdJSON,
			"idshape": cmdIDShape,
			"slice":   cmdSlice,
		},
	})
}

// setup gives every script an isolated HOME and git identity, a fixed clock,
// and no inherited actor variables.
func setup(e *testscript.Env) error {
	home := filepath.Join(e.WorkDir, ".home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	gitconfig := filepath.Join(home, ".gitconfig")
	cfg := "[user]\n\tname = tester\n\temail = tester@example.com\n[init]\n\tdefaultBranch = main\n"
	if err := os.WriteFile(gitconfig, []byte(cfg), 0o644); err != nil {
		return err
	}
	e.Setenv("HOME", home)
	e.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	e.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	e.Setenv("TESS_NOW", "2026-01-01T00:00:00Z")
	for _, v := range []string{"TESS_ACTOR", "CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_REMOTE", "NO_COLOR"} {
		e.Setenv(v, "")
	}
	return nil
}

// capture VAR [PATH] -- tess args...
// Runs tess, parses stdout as JSON, and sets $VAR to the value at PATH
// (default .id). Fails if the command fails.
func cmdCapture(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("capture does not support !")
	}
	if len(args) < 3 {
		ts.Fatalf("usage: capture VAR [PATH] -- cmd args...")
	}
	name, path := args[0], ".id"
	rest := args[1:]
	if rest[0] != "--" {
		path, rest = rest[0], rest[1:]
	}
	if len(rest) < 2 || rest[0] != "--" {
		ts.Fatalf("usage: capture VAR [PATH] -- cmd args...")
	}
	ts.Check(ts.Exec(rest[1], rest[2:]...))
	v := lookup(ts, decode(ts, ts.ReadFile("stdout")), path)
	s, ok := v.(string)
	if !ok {
		ts.Fatalf("capture: %s is %v, not a string", path, v)
	}
	ts.Setenv(name, s)
}

// getjson VAR FILE [PATH]
// Sets $VAR to the string at PATH (default .id) in a JSON file such as
// stdout from the previous exec.
func cmdGetJSON(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) < 2 || len(args) > 3 {
		ts.Fatalf("usage: getjson VAR FILE [PATH]")
	}
	path := ".id"
	if len(args) == 3 {
		path = args[2]
	}
	v := lookup(ts, decode(ts, ts.ReadFile(args[1])), path)
	s, ok := v.(string)
	if !ok {
		ts.Fatalf("getjson: %s is %v, not a string", path, v)
	}
	ts.Setenv(args[0], s)
}

// slice VAR VALUE START [END]
// Sets $VAR to VALUE[START:END] (byte offsets; END defaults to len).
func cmdSlice(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) < 3 || len(args) > 4 {
		ts.Fatalf("usage: slice VAR VALUE START [END]")
	}
	v := args[1]
	start, err := strconv.Atoi(args[2])
	ts.Check(err)
	end := len(v)
	if len(args) == 4 {
		end, err = strconv.Atoi(args[3])
		ts.Check(err)
	}
	if start < 0 || end > len(v) || start > end {
		ts.Fatalf("slice: [%d:%d] out of range for %q", start, end, v)
	}
	ts.Setenv(args[0], v[start:end])
}

// json FILE PATH [has] WANT
// Asserts that the value at PATH in the JSON file (stdout, stderr, or a file)
// equals WANT, or, with "has", that the array at PATH contains WANT. PATH is
// "." or a sequence of .field, [index], and [*] steps, optionally ending
// in "|length". WANT is parsed as JSON if it parses (numbers, true, null, "s",
// [...], {...}); otherwise it's a bare string.
func cmdJSON(ts *testscript.TestScript, neg bool, args []string) {
	has := false
	if len(args) == 4 && args[2] == "has" {
		has, args = true, []string{args[0], args[1], args[3]}
	}
	if len(args) != 3 {
		ts.Fatalf("usage: json FILE PATH [has] WANT")
	}
	got := lookup(ts, decode(ts, ts.ReadFile(args[0])), args[1])
	want := parseWant(args[2])
	var ok bool
	if has {
		arr, isArr := got.([]any)
		if !isArr {
			ts.Fatalf("json: %s is not an array: %s", args[1], show(got))
		}
		for _, el := range arr {
			if equal(el, want) {
				ok = true
				break
			}
		}
	} else {
		ok = equal(got, want)
	}
	if ok == neg {
		verb := "equal"
		if has {
			verb = "contain"
		}
		not := ""
		if neg {
			not = "not "
		}
		ts.Fatalf("json: %s = %s; want it %sto %s %s", args[1], show(got), not, verb, show(want))
	}
}

var idRE = regexp.MustCompile(`^[a-z][a-z0-9]*-[0-9a-z]{3,}$`)

// idshape VALUE: asserts VALUE is a flat issue ID (README "ID shape").
func cmdIDShape(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 1 {
		ts.Fatalf("usage: idshape VALUE")
	}
	if idRE.MatchString(args[0]) == neg {
		ts.Fatalf("idshape: %q does not match %s (neg=%v)", args[0], idRE, neg)
	}
}

func decode(ts *testscript.TestScript, s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		ts.Fatalf("invalid JSON: %v\n%s", err, s)
	}
	return v
}

var stepRE = regexp.MustCompile(`^(\.[A-Za-z_][A-Za-z0-9_]*|\.?\[\d+\]|\.?\[\*\])`)

// lookup evaluates PATH: "." or steps of .field, [index], and [*] (map the
// rest of the path over an array), optionally ending in "|length".
func lookup(ts *testscript.TestScript, v any, path string) any {
	length := false
	if strings.HasSuffix(path, "|length") {
		length, path = true, strings.TrimSuffix(path, "|length")
	}
	if path != "." {
		v = walk(ts, v, path, path)
	}
	if length {
		switch x := v.(type) {
		case []any:
			return float64(len(x))
		case map[string]any:
			return float64(len(x))
		case string:
			return float64(len(x))
		default:
			ts.Fatalf("path %q: can't take length of %s", path, show(v))
		}
	}
	return v
}

func walk(ts *testscript.TestScript, v any, path, rest string) any {
	if rest == "" {
		return v
	}
	m := stepRE.FindString(rest)
	if m == "" {
		ts.Fatalf("bad JSON path %q at %q", path, rest)
	}
	rest = rest[len(m):]
	switch {
	case m == "[*]":
		arr, ok := v.([]any)
		if !ok {
			ts.Fatalf("path %q: %s is not an array", path, show(v))
		}
		out := make([]any, len(arr))
		for i, el := range arr {
			out[i] = walk(ts, el, path, rest)
		}
		return out
	case m[0] == '.':
		obj, ok := v.(map[string]any)
		if !ok {
			ts.Fatalf("path %q: %s is not an object", path, show(v))
		}
		field, present := obj[m[1:]]
		if !present {
			ts.Fatalf("path %q: no field %q in %s", path, m[1:], show(v))
		}
		return walk(ts, field, path, rest)
	default:
		i, _ := strconv.Atoi(m[1 : len(m)-1])
		arr, ok := v.([]any)
		if !ok || i >= len(arr) {
			ts.Fatalf("path %q: no index %d in %s", path, i, show(v))
		}
		return walk(ts, arr[i], path, rest)
	}
}

func parseWant(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		return v
	}
	return s
}

func equal(a, b any) bool { return show(a) == show(b) }

func show(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
