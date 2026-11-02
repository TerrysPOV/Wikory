// Package injectcheck exists only so scripts/inject_selftest.sh can prove that inject.sh
// reports compile errors and panics as INCONCLUSIVE, never as RED.
package injectcheck

// Add returns a + b.
func Add(a, b int) int { return a + b }

// Div returns a / b.
func Div(a, b int) int { return a / b }
