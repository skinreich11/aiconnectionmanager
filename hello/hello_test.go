package hello

import "testing"

func TestHello(t *testing.T) {
	if got := Hello(); got != "Hello, World!" {
		t.Fatalf("Hello() = %q, want %q", got, "Hello, World!")
	}
}
