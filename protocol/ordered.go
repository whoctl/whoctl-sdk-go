package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// Map is a JSON object that remembers the order its keys arrived in.
//
// # Why not map[string]any
//
// A spec is a manifest. `whoctl get linux/user alice -o yaml` has always
// printed its fields in the order the provider declared them — uid before
// shell, not alphabetically — because Spec was a typed struct and the yaml
// encoder followed the struct. Once a provider answers from another process the
// typed struct is on the far side, and decoding into map[string]any would sort
// every manifest whoctl prints.
//
// So the wire keeps the order JSON already has, and Map is what carries it: the
// same field order the provider declared, all the way to the terminal.
type Map struct {
	keys   []string
	values map[string]any
}

// NewMap builds an empty Map.
func NewMap() *Map { return &Map{values: map[string]any{}} }

// Set appends a key, or replaces one already present without moving it.
func (m *Map) Set(key string, value any) {
	if m.values == nil {
		m.values = map[string]any{}
	}
	if _, exists := m.values[key]; !exists {
		m.keys = append(m.keys, key)
	}
	m.values[key] = value
}

// Keys returns the keys in order.
func (m *Map) Keys() []string {
	if m == nil {
		return nil
	}
	return m.keys
}

// Len reports how many keys the map has.
func (m *Map) Len() int {
	if m == nil {
		return 0
	}
	return len(m.keys)
}

// Field resolves one member by name, which is what makes core.Lookup — and so
// every table column — work against a decoded object exactly as it works
// against a typed struct.
func (m *Map) Field(name string) (any, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m.values[name]
	return v, ok
}

// UnmarshalJSON decodes an object, keeping the order of its keys and recursing
// into nested objects so a nested spec is ordered too.
func (m *Map) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok != json.Delim('{') {
		return fmt.Errorf("expected an object, got %v", tok)
	}
	*m = Map{values: map[string]any{}}
	return m.decodeInto(dec)
}

func (m *Map) decodeInto(dec *json.Decoder) error {
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("expected an object key, got %v", tok)
		}
		value, err := decodeValue(dec)
		if err != nil {
			return err
		}
		m.Set(key, value)
	}
	// Consume the closing brace.
	_, err := dec.Token()
	return err
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		nested := NewMap()
		if err := nested.decodeInto(dec); err != nil {
			return nil, err
		}
		return nested, nil
	case json.Delim('['):
		var out []any
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return out, nil
	}
	return normalizeNumber(tok), nil
}

// normalizeNumber turns json.Number into the narrowest thing that is still
// right. JSON has one number type; a uid printed as "1000.000000" would be a
// regression nobody would call cosmetic.
func normalizeNumber(tok json.Token) any {
	num, ok := tok.(json.Number)
	if !ok {
		return tok
	}
	if i, err := num.Int64(); err == nil {
		return i
	}
	f, err := num.Float64()
	if err != nil {
		return num.String()
	}
	return f
}

// MarshalJSON writes the object in key order.
func (m *Map) MarshalJSON() ([]byte, error) {
	if m == nil || len(m.keys) == 0 {
		return []byte("{}"), nil
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		value, err := json.Marshal(m.values[k])
		if err != nil {
			return nil, err
		}
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// MarshalYAML renders the object in key order, which is the whole reason this
// type exists.
func (m *Map) MarshalYAML() (any, error) {
	if m == nil {
		return nil, nil
	}
	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range m.keys {
		key := &yaml.Node{}
		if err := key.Encode(k); err != nil {
			return nil, err
		}
		value := &yaml.Node{}
		if err := value.Encode(m.values[k]); err != nil {
			return nil, err
		}
		node.Content = append(node.Content, key, value)
	}
	return node, nil
}

// UnmarshalYAML accepts a mapping, so a manifest read from disk keeps its own
// order on the way to a provider.
func (m *Map) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("expected a mapping, got %v", node.Kind)
	}
	*m = Map{values: map[string]any{}}
	for i := 0; i+1 < len(node.Content); i += 2 {
		var key string
		if err := node.Content[i].Decode(&key); err != nil {
			return err
		}
		var value any
		if err := node.Content[i+1].Decode(&value); err != nil {
			return err
		}
		m.Set(key, value)
	}
	return nil
}

// MapFrom converts any value — typically a provider's typed spec or status —
// into an ordered Map by round-tripping it through JSON. The struct's field
// order is what JSON writes, so that is the order the Map keeps.
func MapFrom(v any) (*Map, error) {
	if v == nil {
		return nil, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	m := NewMap()
	if err := json.Unmarshal(data, m); err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
}
