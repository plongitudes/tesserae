package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// jsonObject is a JSON object that remembers the order of its keys, so a
// file someone else wrote can be changed without reshuffling it. The other
// JSON values are []any, string, json.Number, bool and nil.
type jsonObject struct {
	keys   []string
	values map[string]any
}

func newJSONObject() *jsonObject {
	return &jsonObject{values: map[string]any{}}
}

func (o *jsonObject) get(key string) (any, bool) {
	v, ok := o.values[key]
	return v, ok
}

// set gives key a value; a new key goes last.
func (o *jsonObject) set(key string, value any) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o *jsonObject) delete(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			return
		}
	}
}

// parseJSONObject reads a file that holds one JSON object. A file with
// nothing in it reads as an empty object.
func parseJSONObject(text []byte) (*jsonObject, error) {
	if len(bytes.TrimSpace(text)) == 0 {
		return newJSONObject(), nil
	}
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()
	value, err := parseJSONValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("there is text after the JSON value")
	}
	object, ok := value.(*jsonObject)
	if !ok {
		return nil, errors.New("it is not a JSON object")
	}
	return object, nil
}

func parseJSONValue(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := newJSONObject()
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return nil, err
			}
			value, err := parseJSONValue(dec)
			if err != nil {
				return nil, err
			}
			object.set(key.(string), value) // the decoder only gives strings as keys
		}
		_, err := dec.Token() // the closing brace
		return object, err
	case json.Delim('['):
		array := []any{}
		for dec.More() {
			value, err := parseJSONValue(dec)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err := dec.Token() // the closing bracket
		return array, err
	}
	return token, nil
}

// formatJSON writes a value the way settings files are usually written:
// two-space indentation, a final newline, and no HTML escaping.
func formatJSON(value any) []byte {
	var b bytes.Buffer
	writeJSONValue(&b, value, "")
	b.WriteByte('\n')
	return b.Bytes()
}

func writeJSONValue(b *bytes.Buffer, value any, indent string) {
	inner := indent + "  "
	switch v := value.(type) {
	case *jsonObject:
		if len(v.keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, key := range v.keys {
			b.WriteString(inner)
			writeJSONScalar(b, key)
			b.WriteString(": ")
			writeJSONValue(b, v.values[key], inner)
			b.WriteString(separator(i, len(v.keys)))
		}
		b.WriteString(indent + "}")
	case []any:
		if len(v) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, element := range v {
			b.WriteString(inner)
			writeJSONValue(b, element, inner)
			b.WriteString(separator(i, len(v)))
		}
		b.WriteString(indent + "]")
	case json.Number:
		b.WriteString(v.String()) // as the file had it
	default:
		writeJSONScalar(b, v)
	}
}

func separator(i, n int) string {
	if i < n-1 {
		return ",\n"
	}
	return "\n"
}

func writeJSONScalar(b *bytes.Buffer, value any) {
	var one strings.Builder
	enc := json.NewEncoder(&one)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		// Only strings, booleans and nil come here; they always encode.
		panic(fmt.Sprintf("encoding %T as JSON: %v", value, err))
	}
	b.WriteString(strings.TrimSuffix(one.String(), "\n"))
}

// jsonEqual reports whether two values are the same JSON, whatever order
// their objects' keys are in.
func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case *jsonObject:
		y, ok := b.(*jsonObject)
		if !ok || len(x.keys) != len(y.keys) {
			return false
		}
		for _, key := range x.keys {
			other, ok := y.get(key)
			if !ok || !jsonEqual(x.values[key], other) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}
