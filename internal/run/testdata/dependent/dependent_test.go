package dependent

import "testing"

func TestHello(t *testing.T) {
	if got := Hello(); got != "hello world" {
		t.Fatalf("Hello() = %q, want %q", got, "hello world")
	}
}
