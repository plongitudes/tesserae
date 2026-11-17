package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/plongitudes/tesserae/internal/tracker"
)

// issueFields are the keys of the issue object, in its order: what
// `show --field` takes. They are read off the type that is marshalled, so a
// key added there is a field here.
var issueFields = func() []string {
	var keys []string
	t := reflect.TypeFor[tracker.Issue]()
	for i := range t.NumField() {
		key, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		keys = append(keys, key)
	}
	return keys
}()

// textFields are the keys whose value is a long text, printed exactly as
// stored.
var textFields = []string{"description", "design", "acceptance_criteria", "notes"}

func checkField(field string) error {
	if slices.Contains(issueFields, field) {
		return nil
	}
	return usage("an issue has no field %q; the fields are %s", field, strings.Join(issueFields, ", "))
}

// printField writes one field of an issue (acceptance/README.md, "Issue
// object"). With --json it is the field's value as JSON. Otherwise a text
// is written exactly as stored, a scalar on a line, a list of scalars one
// to a line, null as nothing, and anything with more structure as JSON. A
// key the issue object leaves out (previous_status of an issue that isn't
// held) reads as null.
func (a *app) printField(issue tracker.Issue, field string) error {
	body, err := json.Marshal(issue)
	if err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return err
	}
	raw, ok := object[field]
	if !ok {
		raw = json.RawMessage("null")
	}
	if a.json {
		return writeJSON(a.stdout, raw)
	}

	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	scalar := func(v any) (string, bool) {
		switch x := v.(type) {
		case string:
			return x, true
		case json.Number:
			return x.String(), true
		case bool:
			return fmt.Sprint(x), true
		}
		return "", false
	}
	if value == nil {
		return nil
	}
	if text, ok := value.(string); ok && slices.Contains(textFields, field) {
		_, err := fmt.Fprint(a.stdout, text)
		return err
	}
	if text, ok := scalar(value); ok {
		_, err := fmt.Fprintln(a.stdout, text)
		return err
	}
	if list, ok := value.([]any); ok {
		lines := make([]string, 0, len(list))
		for _, item := range list {
			line, ok := scalar(item)
			if !ok {
				lines = nil
				break
			}
			lines = append(lines, line+"\n")
		}
		if lines != nil {
			_, err := fmt.Fprint(a.stdout, strings.Join(lines, ""))
			return err
		}
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		return err
	}
	_, err = fmt.Fprintln(a.stdout, indented.String())
	return err
}
