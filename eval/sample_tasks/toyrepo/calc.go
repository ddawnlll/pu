package toycalc

// Add returns a + b.
func Add(a, b int) int {
	return a + b
}

// Sub returns a - b. (Bug: currently adds instead of subtracting.)
func Sub(a, b int) int {
	return a + b
}

// Clamp restricts v to the inclusive range [lo, hi]. (Bug: currently returns v unchanged.)
func Clamp(v, lo, hi int) int {
	return v
}
