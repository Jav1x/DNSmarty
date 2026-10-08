package lru

import "testing"

func TestBasics(t *testing.T) {
	c := New[string, int](3)
	c.Add("a", 1)
	c.Add("b", 2)
	c.Add("c", 3)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("a: %v %v", v, ok)
	}
	// "a" moved to the front, so "b" is the victim.
	c.Add("d", 4)
	if _, ok := c.Get("b"); ok {
		t.Fatal("b не вытеснен")
	}
	if c.Len() != 3 {
		t.Fatalf("len=%d", c.Len())
	}
}

func TestReplace(t *testing.T) {
	c := New[string, int](2)
	c.Add("a", 1)
	c.Add("a", 2)
	if v, _ := c.Get("a"); v != 2 {
		t.Fatalf("не перезаписан: %d", v)
	}
	if c.Len() != 1 {
		t.Fatalf("len=%d", c.Len())
	}
	// A replacement must not grow the cache.
	c.Add("b", 1)
	c.Add("a", 9)
	c.Add("c", 1)
	if c.Len() != 2 {
		t.Fatalf("len=%d", c.Len())
	}
}

func TestRemove(t *testing.T) {
	c := New[int, string](2)
	c.Add(1, "x")
	c.Remove(1)
	c.Remove(99)
	if _, ok := c.Get(1); ok || c.Len() != 0 {
		t.Fatal("не удалён")
	}
}
