package service

import "testing"

func TestQueryVectors_KeepsTheNewestUpToItsSize(t *testing.T) {
	c := queryVectors{max: 2}
	c.put("a", []float32{1})
	c.put("b", []float32{2})
	c.put("A", []float32{9}) // already there: neither replaced nor counted twice
	c.put("c", []float32{3})

	if _, ok := c.get("a"); ok {
		t.Error("the oldest question should have been dropped")
	}
	if v, ok := c.get("b"); !ok || v[0] != 2 {
		t.Errorf("b = %v %v", v, ok)
	}
	if v, ok := c.get(" C "); !ok || v[0] != 3 {
		t.Errorf("c = %v %v", v, ok)
	}
}
