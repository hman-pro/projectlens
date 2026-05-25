package jobs

// BuildArgs returns a fresh argv that begins with the Spec's own Args
// and then appends explicit flags from the target. In project mode
// (ProjectSlug != ""), --project (and --projects when set) replace
// --repo to avoid the --project/--repo mutex error. --config and --db
// are always appended. The Spec is not mutated.
func BuildArgs(spec Spec, t RunnerTarget) []string {
	out := make([]string, 0, len(spec.Args)+8)
	out = append(out, spec.Args...)
	out = append(out,
		"--config", t.ConfigPath,
		"--db", t.DatabaseURL,
	)
	if t.ProjectSlug != "" {
		out = append(out, "--project", t.ProjectSlug)
		if t.ProjectsPath != "" {
			out = append(out, "--projects", t.ProjectsPath)
		}
	} else {
		out = append(out, "--repo", t.RepoPath)
	}
	return out
}
