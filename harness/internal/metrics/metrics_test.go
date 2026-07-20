package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrackerLifecycle(t *testing.T) {
	tracker := NewTracker()

	tracker.StartStep("detect")
	time.Sleep(10 * time.Millisecond)
	tracker.EndStep()

	tracker.StartStep("plan")
	time.Sleep(10 * time.Millisecond)
	tracker.EndStep()

	m := tracker.Generate("completed", "test-model", "test-provider")

	if m.Status != "completed" {
		t.Errorf("status = %q", m.Status)
	}
	if len(m.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(m.Steps))
	}
	if m.Steps[0].Step != "detect" {
		t.Errorf("step[0] = %q", m.Steps[0].Step)
	}
	if m.Steps[0].Seconds <= 0 {
		t.Error("expected positive duration for detect")
	}
	if m.Seconds <= 0 {
		t.Error("expected positive total duration")
	}
}

func TestTrackerAutoEndOnGenerate(t *testing.T) {
	tracker := NewTracker()
	tracker.StartStep("detect")
	m := tracker.Generate("completed", "test-model", "test-provider")

	if len(m.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(m.Steps))
	}
}

func TestTrackerAutoEndOnStartStep(t *testing.T) {
	tracker := NewTracker()
	tracker.StartStep("detect")
	tracker.StartStep("plan")
	tracker.EndStep()

	m := tracker.Generate("completed", "test-model", "test-provider")
	if len(m.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(m.Steps))
	}
}

func TestStepDuration(t *testing.T) {
	tracker := NewTracker()
	tracker.StartStep("detect")
	time.Sleep(50 * time.Millisecond)
	tracker.EndStep()

	d := tracker.StepDuration("detect")
	if d < 0 {
		t.Errorf("duration = %d, want >= 0", d)
	}

	if tracker.StepDuration("nonexistent") != 0 {
		t.Error("expected 0 for nonexistent step")
	}
}

func TestWriteMetrics(t *testing.T) {
	dir := t.TempDir()
	m := &Metrics{
		Status:    "completed",
		StartedAt: time.Now(),
		EndedAt:   time.Now(),
		Seconds:   42.5,
		Steps: []StepTiming{
			{Step: "detect", Seconds: 10},
		},
	}

	err := WriteMetrics(dir, m)
	if err != nil {
		t.Fatalf("WriteMetrics: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "metrics.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var loaded Metrics
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if loaded.Status != "completed" {
		t.Errorf("status = %q", loaded.Status)
	}
	if loaded.Seconds != 42.5 {
		t.Errorf("seconds = %f", loaded.Seconds)
	}
}
