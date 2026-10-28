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

// Package diagnostics captures controller runtime state for OOM investigations.
package diagnostics

import (
	"context"
	"fmt"
	"net"
	"runtime/metrics"
	"strconv"
	"time"

	"github.com/go-logr/logr"
)

// ValidatePprofAddress keeps unauthenticated runtime profiles on loopback.
func ValidatePprofAddress(address string) error {
	if address == "" || address == "0" {
		return nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("pprof address must be a loopback IP and port: %w", err)
	}
	ip := net.ParseIP(host)
	number, portErr := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || portErr != nil || number < 0 || number > 65535 {
		return fmt.Errorf("pprof address %q must use a loopback IP and valid port", address)
	}
	return nil
}

// MemorySampler writes an initial sample and periodic samples until shutdown.
// Its storage is fixed-size; it keeps no sample history in the controller.
// The log collector, when configured, supplies retention across pod restarts.
type MemorySampler struct {
	Interval time.Duration
	Log      logr.Logger
}

func (MemorySampler) NeedLeaderElection() bool { return false }

// Capture writes one sample without waiting for the manager or its cache.
func (s MemorySampler) Capture() {
	fields := []struct{ name, metric string }{
		{"heap_alloc_bytes", "/memory/classes/heap/objects:bytes"},
		{"heap_live_bytes", "/gc/heap/live:bytes"},
		{"heap_goal_bytes", "/gc/heap/goal:bytes"},
		{"heap_objects", "/gc/heap/objects:objects"},
		{"runtime_total_bytes", "/memory/classes/total:bytes"},
		{"heap_released_bytes", "/memory/classes/heap/released:bytes"},
		{"gc_cycles", "/gc/cycles/total:gc-cycles"},
		{"goroutines", "/sched/goroutines:goroutines"},
	}
	samples := make([]metrics.Sample, len(fields))
	for i, field := range fields {
		samples[i].Name = field.metric
	}
	metrics.Read(samples)
	values := make([]any, 0, 2*len(samples))
	for i, sample := range samples {
		if sample.Value.Kind() == metrics.KindUint64 {
			values = append(values, fields[i].name, sample.Value.Uint64())
		}
	}
	s.Log.Info("Controller memory sample", values...)
}

func (s MemorySampler) Start(ctx context.Context) error {
	if s.Interval < 0 {
		return fmt.Errorf("memory log interval must not be negative")
	}
	if s.Interval == 0 || ctx.Err() != nil {
		return nil
	}
	s.Capture()
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.Capture()
		}
	}
}
