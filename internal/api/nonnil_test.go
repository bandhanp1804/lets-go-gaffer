package api

import (
	"encoding/json"
	"testing"
)

type leaf struct {
	Tags []string `json:"tags"`
}

type shape struct {
	List    []string          `json:"list"`
	Opt     []string          `json:"opt,omitempty"`
	Table   map[string]int    `json:"table"`
	Leaves  []leaf            `json:"leaves"`
	One     leaf              `json:"one"`
	Ptr     *leaf             `json:"ptr"`
	NilPtr  *leaf             `json:"nil_ptr"`
	Any     any               `json:"any"`
	Raw     json.RawMessage   `json:"raw"`
	Nested  map[string][]leaf `json:"nested"`
	private []string
}

func encode(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNonNil_EveryNilListAndMapBecomesEmptyAtAnyDepth(t *testing.T) {
	in := shape{Leaves: []leaf{{}, {Tags: []string{"a"}}}, Ptr: &leaf{}, Any: leaf{}, Raw: json.RawMessage(`{"x":1}`),
		Nested: map[string][]leaf{"k": {{}}}}

	got := encode(t, nonNil(in))

	want := `{"list":[],"table":{},"leaves":[{"tags":[]},{"tags":["a"]}],"one":{"tags":[]},"ptr":{"tags":[]},"nil_ptr":null,"any":{"tags":[]},"raw":{"x":1},"nested":{"k":[{"tags":[]}]}}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestNonNil_AValueThatEncodesToNullWithoutIt(t *testing.T) {
	if got := encode(t, shape{}); got == encode(t, nonNil(shape{})) {
		t.Fatalf("the test value must contain nils to prove anything: %s", got)
	}
}

func TestNonNil_DoesNotEditItsInput(t *testing.T) {
	shared := []leaf{{}, {}}
	in := shape{Leaves: shared}

	nonNil(in)

	if shared[0].Tags != nil || in.List != nil {
		t.Error("the input was modified: views share slices with cached data, so this would be a race")
	}
}

func TestNonNil_OptionalEmptyListsStayOmitted(t *testing.T) {
	if got := encode(t, nonNil(struct {
		Opt []string `json:"opt,omitempty"`
	}{})); got != `{}` {
		t.Errorf("omitempty must still omit: %s", got)
	}
}

func TestNonNil_PlainValuesPassThrough(t *testing.T) {
	for _, v := range []any{nil, 5, "x", map[string]string{"a": "b"}} {
		if encode(t, nonNil(v)) != encode(t, v) {
			t.Errorf("%v changed", v)
		}
	}
}
