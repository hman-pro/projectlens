package mcpserver

import (
	"encoding/json"
	"testing"
)

func TestEvidenceSpanJSONShape(t *testing.T) {
	e := EvidenceSpan{FilePath: "internal/foo/bar.go", LineStart: 10, LineEnd: 20}
	got, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"file_path":"internal/foo/bar.go","line_start":10,"line_end":20}`
	if string(got) != want {
		t.Fatalf("EvidenceSpan JSON shape:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestDegradationJSONShape(t *testing.T) {
	d := Degradation{Degraded: true, Reason: "embedder unreachable", Fallback: "lexical-only"}
	got, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"degraded":true,"reason":"embedder unreachable","fallback":"lexical-only"}`
	if string(got) != want {
		t.Fatalf("Degradation JSON shape:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestStageFreshnessJSONShape(t *testing.T) {
	t.Run("zero value omits optional fields", func(t *testing.T) {
		got, err := json.Marshal(StageFreshness{})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		want := `{"stage":"","status":""}`
		if string(got) != want {
			t.Fatalf("zero StageFreshness JSON:\n  got:  %s\n  want: %s", got, want)
		}
	})

	t.Run("populated emits all fields", func(t *testing.T) {
		s := StageFreshness{
			Stage:          "code",
			Status:         "completed",
			CommitSHA:      "abc1234",
			StartedAt:      "2026-05-18T10:00:00Z",
			CompletedAt:    "2026-05-18T10:05:00Z",
			AgeMinutes:     12.5,
			FilesProcessed: 42,
		}
		got, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		want := `{"stage":"code","status":"completed","commit_sha":"abc1234","started_at":"2026-05-18T10:00:00Z","completed_at":"2026-05-18T10:05:00Z","age_minutes":12.5,"files_processed":42}`
		if string(got) != want {
			t.Fatalf("populated StageFreshness JSON:\n  got:  %s\n  want: %s", got, want)
		}
	})
}

func TestProviderHealthTristate(t *testing.T) {
	cases := []struct {
		name string
		p    ProviderHealth
		want string
	}{
		{"reachable", ProviderHealth{Role: "embedder", Provider: "ollama", State: "reachable"}, `{"role":"embedder","provider":"ollama","state":"reachable"}`},
		{"configured", ProviderHealth{Role: "summarizer", Provider: "anthropic", State: "configured"}, `{"role":"summarizer","provider":"anthropic","state":"configured"}`},
		{"error", ProviderHealth{Role: "embedder", Provider: "ollama", State: "error", Error: "dial tcp: connection refused"}, `{"role":"embedder","provider":"ollama","state":"error","error":"dial tcp: connection refused"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.p)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("ProviderHealth JSON:\n  got:  %s\n  want: %s", got, tc.want)
			}
		})
	}
}
