package confine

// SetStepHook installs a hook that ReplaceIfUnchanged calls after each of its steps
// ("moved", "compared", "installed"). It exists for crash tests in other packages. Never
// call it outside tests.
func SetStepHook(f func(step string)) { stepHook = f }

func step(s string) {
	if stepHook != nil {
		stepHook(s)
	}
}
