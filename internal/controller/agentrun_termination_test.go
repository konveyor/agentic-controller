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

package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestPodTerminationMessage(t *testing.T) {
	const msg = `{"reason":"UnsupportedSourceSCM","message":"only git is supported"}`

	terminatedAgent := corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: agentContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{Message: msg, ExitCode: 1},
					},
				},
			},
		},
	}
	runningAgent := corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: agentContainerName, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			},
		},
	}
	terminatedOther := corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "sidecar",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{Message: "not the agent"},
					},
				},
			},
		},
	}

	tests := []struct {
		name string
		pods []corev1.Pod
		want string
	}{
		{"terminated agent container", []corev1.Pod{terminatedAgent}, msg},
		{"running agent container", []corev1.Pod{runningAgent}, ""},
		{"terminated non-agent container ignored", []corev1.Pod{terminatedOther}, ""},
		{"no pods", nil, ""},
		{"agent found among several pods", []corev1.Pod{runningAgent, terminatedAgent}, msg},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := podTerminationMessage(tt.pods); got != tt.want {
				t.Errorf("podTerminationMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}
