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

// Package controller implements the reconciliation logic for konveyor.io CRDs.
package controller

const (
	// ConditionTypeReady indicates whether the resource is ready.
	ConditionTypeReady = "Ready"

	// labelManagedBy is the standard Kubernetes label key for managed-by.
	labelManagedBy = "app.kubernetes.io/managed-by"

	// managedByLabel is the value used for app.kubernetes.io/managed-by labels.
	managedByLabel = "agentic-controller"

	// jobConditionSuccessCriteriaMet is the K8s 1.36+ condition required
	// alongside JobComplete for valid Job status updates.
	jobConditionSuccessCriteriaMet = "SuccessCriteriaMet"
)
