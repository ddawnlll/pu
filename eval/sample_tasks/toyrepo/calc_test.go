package toycalc

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}

func TestSub(t *testing.T) {
	if got := Sub(10, 4); got != 6 {
		t.Fatalf("Sub(10, 4) = %d, want 6", got)
	}
}

func TestClamp(t *testing.T) {
	if got := Clamp(-5, 0, 10); got != 0 {
		t.Fatalf("Clamp(-5, 0, 10) = %d, want 0", got)
	}
	if got := Clamp(15, 0, 10); got != 10 {
		t.Fatalf("Clamp(15, 0, 10) = %d, want 10", got)
	}
	if got := Clamp(7, 0, 10); got != 7 {
		t.Fatalf("Clamp(7, 0, 10) = %d, want 7", got)
	}
}
