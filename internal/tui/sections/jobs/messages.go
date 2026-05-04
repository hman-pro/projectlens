package jobs

const ID = "jobs"

type RefreshedMsg struct {
	Runs []JobRun
	Err  error
	Gen  uint64
}
