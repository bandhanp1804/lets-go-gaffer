package tsgen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/tsgen"
)

type Inner struct {
	N int `json:"n"`
}

type Base struct {
	ID int `json:"id"`
}

type Sample struct {
	Base                      // flattened, like encoding/json
	Text     string           `json:"text"`
	Flag     bool             `json:"flag"`
	Price    float64          `json:"price"`
	Maybe    *string          `json:"maybe"`
	Opt      string           `json:"opt,omitempty"`
	OptList  []string         `json:"opt_list,omitempty"`
	OptPtr   *int             `json:"opt_ptr,omitempty"`
	List     []string         `json:"list"`
	Nested   Inner            `json:"nested"`
	Inners   []Inner          `json:"inners"`
	Table    map[string][]int `json:"table"`
	Pairs    [2]int           `json:"pairs"`
	Anything any              `json:"anything"`
	Maybes   []*string        `json:"maybes"`
	Skipped  string           `json:"-"`
	hidden   string           //nolint:unused // unexported fields are not in JSON
	NoTag    string
}

func gen(t *testing.T, name func(reflect.Type) string, vs ...any) (string, error) {
	t.Helper()
	g := &tsgen.Generator{Name: name}
	for _, v := range vs {
		if err := g.Add(v); err != nil {
			return "", err
		}
	}
	var sb strings.Builder
	err := g.Write(&sb)
	return sb.String(), err
}

func TestGenerate_FollowsEncodingJSON(t *testing.T) {
	got, err := gen(t, nil, Sample{})
	if err != nil {
		t.Fatal(err)
	}
	want := `export interface Sample {
  id: number;
  text: string;
  flag: boolean;
  price: number;
  maybe: string | null;
  opt?: string;
  opt_list?: string[];
  opt_ptr?: number;
  list: string[] | null;
  nested: Inner;
  inners: Inner[] | null;
  table: Record<string, number[] | null> | null;
  pairs: number[];
  anything: unknown;
  maybes: (string | null)[] | null;
  NoTag: string;
}

export interface Inner {
  n: number;
}
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestGenerate_CustomNamesApplyToDeclarationsAndReferences(t *testing.T) {
	name := func(t reflect.Type) string { return strings.TrimSuffix(t.Name(), "er") }
	got, err := gen(t, name, Sample{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "export interface Inn {") || !strings.Contains(got, "nested: Inn;") {
		t.Errorf("renaming must reach both the declaration and the reference:\n%s", got)
	}
}

func TestGenerate_EachStructIsWrittenOnceEvenWhenReferencedTwice(t *testing.T) {
	type Twice struct {
		A Inner   `json:"a"`
		B []Inner `json:"b"`
	}
	got, _ := gen(t, nil, Twice{}, Inner{})
	if n := strings.Count(got, "export interface Inner"); n != 1 {
		t.Errorf("Inner written %d times", n)
	}
}

func TestGenerate_RecursiveTypesTerminate(t *testing.T) {
	type Tree struct {
		Kids []*Tree `json:"kids"`
	}
	got, err := gen(t, nil, Tree{})
	if err != nil || !strings.Contains(got, "kids: (Tree | null)[] | null;") {
		t.Errorf("%v\n%s", err, got)
	}
}

func TestGenerate_RejectsWhatJSONCannotRepresent(t *testing.T) {
	type Chan struct {
		C chan int `json:"c"`
	}
	type IntKeys struct {
		M map[int]string `json:"m"`
	}
	for _, v := range []any{Chan{}, IntKeys{}} {
		if _, err := gen(t, nil, v); err == nil {
			t.Errorf("%T was accepted", v)
		}
	}
}

func TestAdd_RejectsNonStructs(t *testing.T) {
	g := &tsgen.Generator{}
	for _, v := range []any{1, "x", []int{}, nil} {
		if err := g.Add(v); err == nil {
			t.Errorf("%T accepted", v)
		}
	}
}

func TestAdd_AcceptsPointers(t *testing.T) {
	if _, err := gen(t, nil, &Inner{}); err != nil {
		t.Error(err)
	}
}

func TestGenerate_TwoTypesWithTheSameTSNameIsAnError(t *testing.T) {
	type A struct {
		X int `json:"x"`
	}
	type B struct {
		Y int `json:"y"`
	}
	same := func(reflect.Type) string { return "Same" }
	if _, err := gen(t, same, A{}, B{}); err == nil {
		t.Error("a name collision was accepted")
	}
}

func TestGenerate_ListsNeverNullDropsTheNullFromSlicesAndMaps(t *testing.T) {
	type S struct {
		List  []string       `json:"list"`
		Table map[string]int `json:"table"`
		Ptr   *string        `json:"ptr"`
	}
	g := &tsgen.Generator{ListsNeverNull: true}
	if err := g.Add(S{}); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := g.Write(&sb); err != nil {
		t.Fatal(err)
	}
	want := "export interface S {\n  list: string[];\n  table: Record<string, number>;\n  ptr: string | null;\n}\n"
	if sb.String() != want {
		t.Errorf("got:\n%s", sb.String())
	}
}
