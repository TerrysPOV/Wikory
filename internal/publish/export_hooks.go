package publish

// SetKillHook lets a test in another package stop a publish at a named step (for example
// "applied-0"), as a kill would. Pass nil to remove it.
func SetKillHook(f func(step string)) {
	if f == nil {
		killHook = func(string) {}
		return
	}
	killHook = f
}
