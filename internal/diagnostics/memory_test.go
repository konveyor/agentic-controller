/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package diagnostics

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
)

func TestMemorySamplerCapturesBeforeStart(t *testing.T) {
	count := 0
	logger := funcr.NewJSON(func(string) { count++ }, funcr.Options{})
	sampler := MemorySampler{Log: logger}
	sampler.Capture()
	if count != 1 {
		t.Fatalf("expected a sample without starting the manager, got %d", count)
	}
}

func TestMemorySamplerCapturesRuntimeAndStops(t *testing.T) {
	messages := make(chan string, 10)
	logger := funcr.NewJSON(func(message string) { messages <- message }, funcr.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (MemorySampler{Interval: 10 * time.Millisecond, Log: logger}).Start(ctx) }()
	// An initial sample and a timed sample both contain actual runtime values.
	for range 2 {
		select {
		case message := <-messages:
			var fields map[string]any
			if err := json.Unmarshal([]byte(message), &fields); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"heap_alloc_bytes", "heap_live_bytes", "heap_goal_bytes", "heap_objects", "runtime_total_bytes", "heap_released_bytes", "gc_cycles", "goroutines"} {
				if _, ok := fields[key].(float64); !ok {
					t.Fatalf("missing numeric %s in %s", key, message)
				}
			}
			if fields["heap_alloc_bytes"].(float64) == 0 || fields["goroutines"].(float64) == 0 {
				t.Fatalf("invalid runtime sample: %s", message)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no memory sample")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sampler did not stop")
	}
}

func TestMemorySamplerDisabledAndInvalidIntervals(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Second} {
		t.Run(interval.String(), func(t *testing.T) {
			logger := funcr.NewJSON(func(string) { t.Error("unexpected memory sample") }, funcr.Options{})
			err := (MemorySampler{Interval: interval, Log: logger}).Start(context.Background())
			if (err != nil) != (interval < 0) {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}

func TestPprofRejectsNonLoopbackListeners(t *testing.T) {
	for _, address := range []string{"", "0", "127.0.0.1:6060", "[::1]:6060"} {
		if err := ValidatePprofAddress(address); err != nil {
			t.Errorf("%q: %v", address, err)
		}
	}
	for _, address := range []string{":6060", "0.0.0.0:6060", "[::]:6060", "192.0.2.1:6060", "localhost:6060", "127.0.0.1:no-port", "127.0.0.1:65536"} {
		if err := ValidatePprofAddress(address); err == nil {
			t.Errorf("accepted non-loopback/invalid address %q", address)
		}
	}
}
