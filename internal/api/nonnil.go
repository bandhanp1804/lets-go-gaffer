package api

import "reflect"

// nonNil returns a copy of v in which every nil slice and map is empty, so that
// encoding/json writes [] and {} instead of null.
//
// The API promises its clients that a list is never null (the generated TypeScript
// types rely on it). Go's nil slices would break that promise wherever a view leaves
// one unset, and such a field is easy to add and forget. This enforces the promise in
// one place instead of in every view.
//
// It copies rather than edits: views can share slices with cached data, and filling a
// shared element in place would be a data race between requests.
func nonNil(v any) any {
	if v == nil {
		return nil
	}
	return filled(reflect.ValueOf(v)).Interface()
}

func filled(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return v // bytes are encoded as a string, not a list
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len()) // empty, not nil, when v is nil
		for i := range v.Len() {
			out.Index(i).Set(filled(v.Index(i)))
		}
		return out
	case reflect.Map:
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		for _, k := range v.MapKeys() {
			out.SetMapIndex(k, filled(v.MapIndex(k)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v) // copies unexported fields too, which JSON never writes
		for i := range v.NumField() {
			if out.Field(i).CanSet() {
				out.Field(i).Set(filled(v.Field(i)))
			}
		}
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v // a nil pointer is null on purpose
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(filled(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(filled(v.Elem()))
		return out
	}
	return v
}
