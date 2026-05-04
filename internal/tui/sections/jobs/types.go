package jobs

import "time"

type JobRun struct {
	Started   time.Time
	Completed time.Time
	Duration  time.Duration
	Action    string
	Status    string
	LogPath   string
}
