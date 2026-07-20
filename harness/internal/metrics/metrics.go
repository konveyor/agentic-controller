package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type StepTiming struct {
	Step      string        `json:"step"`
	StartedAt time.Time     `json:"started_at"`
	EndedAt   time.Time     `json:"ended_at"`
	Duration  time.Duration `json:"-"`
	Seconds   float64       `json:"duration_seconds"`
}

type Metrics struct {
	Status    string       `json:"status"`
	Model     string       `json:"model,omitempty"`
	Provider  string       `json:"provider,omitempty"`
	StartedAt time.Time    `json:"started_at"`
	EndedAt   time.Time    `json:"ended_at"`
	Seconds   float64      `json:"total_seconds"`
	Steps     []StepTiming `json:"steps"`
}

type Tracker struct {
	startedAt time.Time
	steps     []StepTiming
	current   *StepTiming
}

func NewTracker() *Tracker {
	return &Tracker{startedAt: time.Now()}
}

func (t *Tracker) StartStep(name string) {
	if t.current != nil {
		t.EndStep()
	}
	t.current = &StepTiming{
		Step:      name,
		StartedAt: time.Now(),
	}
}

func (t *Tracker) EndStep() {
	if t.current == nil {
		return
	}
	t.current.EndedAt = time.Now()
	t.current.Duration = t.current.EndedAt.Sub(t.current.StartedAt)
	t.current.Seconds = t.current.Duration.Seconds()
	t.steps = append(t.steps, *t.current)
	t.current = nil
}

func (t *Tracker) Generate(status, model, provider string) *Metrics {
	if t.current != nil {
		t.EndStep()
	}
	now := time.Now()
	return &Metrics{
		Status:    status,
		Model:     model,
		Provider:  provider,
		StartedAt: t.startedAt,
		EndedAt:   now,
		Seconds:   now.Sub(t.startedAt).Seconds(),
		Steps:     t.steps,
	}
}

func (t *Tracker) StepDuration(name string) int {
	for _, s := range t.steps {
		if s.Step == name {
			return int(s.Seconds)
		}
	}
	return 0
}

func WriteMetrics(runDir string, m *Metrics) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metrics: %w", err)
	}
	return os.WriteFile(filepath.Join(runDir, "metrics.json"), data, 0644)
}
