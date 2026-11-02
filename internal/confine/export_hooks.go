package confine

// SetStepHook installs a hook that ReplaceIfUnchanged calls after each of its steps
// ("moved", "compared", "installed"). TEST-ONLY: it exists for crash tests in other
// packages and sets a package-level variable, so it is exported in the production build
// too; production code must never call it.
func SetStepHook(f func(step string)) { stepHook = f }

func step(s string) {
	if stepHook != nil {
		stepHook(s)
	}
}
