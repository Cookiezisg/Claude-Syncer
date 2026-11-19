// Package jsonobj is a JSON object that keeps its keys in file order.
//
// Claude Desktop writes session records as compact JSON with keys in
// insertion order. Round-tripping through map[string]any would reorder every
// key, so records we touch would no longer look like records the app wrote.
// Object keeps the order and the exact (compacted) bytes of every value.
package jsonobj

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Field is one key and its raw, compacted JSON value.
type Field struct {
	Key   string
	Value json.RawMessage
}

// Object is an ordered JSON object.
type Object struct {
	fields []Field
	index  map[string]int
}

// New returns an empty object.
func New() *Object { return &Object{index: map[string]int{}} }

// Parse decodes a JSON object, keeping key order. A repeated key keeps its
// first position and its last value, as JSON.parse does with values.
func Parse(data []byte) (*Object, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	o := New()
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected token %v", tok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if err := o.SetRaw(key, raw); err != nil {
			return nil, err
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("trailing data after object")
	}
	return o, nil
}

// Len is the number of keys.
func (o *Object) Len() int { return len(o.fields) }

// Keys returns the keys in order.
func (o *Object) Keys() []string {
	keys := make([]string, len(o.fields))
	for i, f := range o.fields {
		keys[i] = f.Key
	}
	return keys
}

// Get returns the raw value of key.
func (o *Object) Get(key string) (json.RawMessage, bool) {
	i, ok := o.index[key]
	if !ok {
		return nil, false
	}
	return o.fields[i].Value, true
}

// Has reports whether key is present.
func (o *Object) Has(key string) bool { _, ok := o.index[key]; return ok }

// SetRaw sets key to a raw JSON value, compacting it. An existing key keeps
// its position; a new key is appended.
func (o *Object) SetRaw(key string, raw json.RawMessage) error {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return fmt.Errorf("value of %q: %w", key, err)
	}
	v := json.RawMessage(buf.Bytes())
	if i, ok := o.index[key]; ok {
		o.fields[i].Value = v
		return nil
	}
	o.index[key] = len(o.fields)
	o.fields = append(o.fields, Field{Key: key, Value: v})
	return nil
}

// Set marshals v and stores it under key.
func (o *Object) Set(key string, v any) error {
	raw, err := marshal(v)
	if err != nil {
		return err
	}
	return o.SetRaw(key, raw)
}

// Delete removes key if present.
func (o *Object) Delete(key string) {
	i, ok := o.index[key]
	if !ok {
		return
	}
	o.fields = append(o.fields[:i], o.fields[i+1:]...)
	delete(o.index, key)
	for j := i; j < len(o.fields); j++ {
		o.index[o.fields[j].Key] = j
	}
}

// String returns the value of key if it is a JSON string.
func (o *Object) String(key string) (string, bool) {
	raw, ok := o.Get(key)
	if !ok {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// Bool returns the value of key if it is a JSON boolean.
func (o *Object) Bool(key string) (bool, bool) {
	raw, ok := o.Get(key)
	if !ok {
		return false, false
	}
	var b bool
	if json.Unmarshal(raw, &b) != nil {
		return false, false
	}
	return b, true
}

// Int returns the value of key if it is a JSON number that fits in int64.
func (o *Object) Int(key string) (int64, bool) {
	raw, ok := o.Get(key)
	if !ok {
		return 0, false
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil {
		return 0, false
	}
	return int64(f), true
}

// Strings returns the value of key if it is an array of strings; non-string
// elements are skipped.
func (o *Object) Strings(key string) []string {
	raw, ok := o.Get(key)
	if !ok {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		var s string
		if json.Unmarshal(it, &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

// IsTruthy reports whether key is present and not null, false, 0 or "".
func (o *Object) IsTruthy(key string) bool {
	raw, ok := o.Get(key)
	if !ok {
		return false
	}
	switch string(raw) {
	case "null", "false", "0", `""`:
		return false
	}
	return true
}

// Marshal encodes the object as compact JSON in key order.
func (o *Object) Marshal() []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range o.fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, _ := marshal(f.Key)
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(f.Value)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// Clone returns a deep copy.
func (o *Object) Clone() *Object {
	c := &Object{fields: make([]Field, len(o.fields)), index: make(map[string]int, len(o.fields))}
	for i, f := range o.fields {
		c.fields[i] = Field{Key: f.Key, Value: append(json.RawMessage(nil), f.Value...)}
		c.index[f.Key] = i
	}
	return c
}

// marshal encodes without HTML escaping, as JSON.stringify does.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
