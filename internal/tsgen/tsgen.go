// Package tsgen writes TypeScript interfaces for Go structs, read from their json
// tags, so a web client's types cannot drift from what the server sends.
//
// The mapping follows encoding/json exactly: a nil slice or map is sent as null, so a
// slice field without omitempty is typed `T[] | null` and the compiler makes the client
// handle it; a field with omitempty may be absent and is optional; embedded structs are
// flattened.
package tsgen

import (
	"fmt"
	"io"
	"reflect"
	"strings"
)

// Generator turns Go types into TypeScript interfaces.
type Generator struct {
	// Name maps a Go type to the TypeScript name; the default is the Go name.
	Name func(reflect.Type) string
	// ListsNeverNull says the server turns nil slices and maps into empty ones before
	// encoding, so they are typed T[] and not T[] | null.
	ListsNeverNull bool

	order []reflect.Type
	seen  map[reflect.Type]bool
}

// Add registers a struct type and, when it is written, every struct it refers to.
func (g *Generator) Add(v any) error {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return fmt.Errorf("tsgen: %T is not a struct", v)
	}
	g.collect(t)
	return nil
}

func (g *Generator) collect(t reflect.Type) {
	if g.seen == nil {
		g.seen = map[reflect.Type]bool{}
	}
	if g.seen[t] {
		return
	}
	g.seen[t] = true
	g.order = append(g.order, t)
	for _, f := range fields(t) {
		g.collectRefs(f.Type)
	}
}

func (g *Generator) collectRefs(t reflect.Type) {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		g.collectRefs(t.Elem())
	case reflect.Struct:
		g.collect(t)
	}
}

func (g *Generator) name(t reflect.Type) string {
	if g.Name != nil {
		return g.Name(t)
	}
	return t.Name()
}

// field is one JSON property of a struct.
type field struct {
	Name      string
	Type      reflect.Type
	OmitEmpty bool
}

// fields lists a struct's JSON properties in declaration order, flattening embedded
// structs the way encoding/json does and skipping unexported and "-" fields.
func fields(t reflect.Type) []field {
	var out []field
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if sf.Anonymous && name == "" {
			ft := sf.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				out = append(out, fields(ft)...)
				continue
			}
		}
		if !sf.IsExported() {
			continue
		}
		if name == "" {
			name = sf.Name
		}
		out = append(out, field{Name: name, Type: sf.Type, OmitEmpty: strings.Contains(opts, "omitempty")})
	}
	return out
}

// Write emits every registered interface, in registration order with the structs they
// refer to following, and fails on a type JSON cannot represent in TypeScript.
func (g *Generator) Write(w io.Writer) error {
	names := map[string]reflect.Type{}
	for _, t := range g.order {
		if other, dup := names[g.name(t)]; dup {
			return fmt.Errorf("tsgen: %s and %s are both named %s", other, t, g.name(t))
		}
		names[g.name(t)] = t
	}
	for i, t := range g.order {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "export interface %s {\n", g.name(t))
		for _, f := range fields(t) {
			ts, err := g.tsType(f.Type)
			if err != nil {
				return fmt.Errorf("tsgen: %s.%s: %w", t.Name(), f.Name, err)
			}
			opt := ""
			if f.OmitEmpty {
				opt = "?"
				ts = strings.TrimSuffix(ts, " | null") // omitted rather than null when empty
			}
			fmt.Fprintf(w, "  %s%s: %s;\n", f.Name, opt, ts)
		}
		fmt.Fprintln(w, "}")
	}
	return nil
}

func (g *Generator) tsType(t reflect.Type) (string, error) {
	switch t.Kind() {
	case reflect.String:
		return "string", nil
	case reflect.Bool:
		return "boolean", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number", nil
	case reflect.Pointer:
		inner, err := g.tsType(t.Elem())
		return inner + " | null", err
	case reflect.Slice, reflect.Array:
		inner, err := g.tsType(t.Elem())
		if err != nil {
			return "", err
		}
		if strings.Contains(inner, " ") { // a union needs brackets before [] is applied
			inner = "(" + inner + ")"
		}
		if t.Kind() == reflect.Slice && !g.ListsNeverNull {
			return inner + "[] | null", nil // a nil slice is encoded as null
		}
		return inner + "[]", nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return "", fmt.Errorf("a map keyed by %s has no JSON form", t.Key())
		}
		inner, err := g.tsType(t.Elem())
		if g.ListsNeverNull {
			return "Record<string, " + inner + ">", err
		}
		return "Record<string, " + inner + "> | null", err
	case reflect.Struct:
		return g.name(t), nil
	case reflect.Interface:
		return "unknown", nil
	}
	return "", fmt.Errorf("%s cannot be represented in JSON", t)
}
