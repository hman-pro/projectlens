package jobs

// BuildArgs returns a fresh argv that begins with the Spec's own Args
// and then appends explicit --config, --db, --repo flags from the
// target. The Spec is not mutated.
func BuildArgs(spec Spec, t RunnerTarget) []string {
	out := make([]string, 0, len(spec.Args)+6)
	out = append(out, spec.Args...)
	out = append(out,
		"--config", t.ConfigPath,
		"--db", t.DatabaseURL,
		"--repo", t.RepoPath,
	)
	return out
}
