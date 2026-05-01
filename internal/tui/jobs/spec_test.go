package jobs_test

import (
	"testing"

	"github.com/hman-pro/projectlens/internal/tui/jobs"
)

func TestConfirmKind_String(t *testing.T) {
	cases := map[jobs.ConfirmKind]string{
		jobs.ConfirmYesNo: "yesno",
		jobs.ConfirmTyped: "typed",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", k, got, want)
		}
	}
}

func TestSpec_ZeroValueIsInvalid(t *testing.T) {
	var s jobs.Spec
	if s.Valid() {
		t.Fatal("zero Spec must not be Valid")
	}
}
