package unrelated

import "testing"

func TestTwo(t *testing.T) {
	if Two() != 2 {
		t.Fatal("Two")
	}
}
