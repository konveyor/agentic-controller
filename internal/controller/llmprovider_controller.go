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
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	// verificationJobPrefix is the prefix for verification Job names.
	verificationJobPrefix = "llm-verify-"

	// DefaultVerificationImage is the default image used for LLM provider
	// verification when no override is configured. In production, the
	// controller should use the agent base image from this repository.
	DefaultVerificationImage = "quay.io/konveyor/agent-base:latest"
)

// LLMProviderReconciler reconciles an LLMProvider object.
type LLMProviderReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// VerificationImage overrides the container image used for
	// connectivity verification Jobs. Defaults to DefaultVerificationImage.
	VerificationImage string
}

// +kubebuilder:rbac:groups=konveyor.io,resources=llmproviders,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=konveyor.io,resources=llmproviders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=konveyor.io,resources=llmproviders/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile handles LLMProvider reconciliation.
//
// The controller verifies provider connectivity by:
//  1. Checking that the referenced credential Secret exists
//  2. Creating a verification Job that tests the endpoint using the
//     agent base image
//  3. Updating status based on the Job result
func (r *LLMProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var provider konveyoriov1alpha1.LLMProvider
	if err := r.Get(ctx, req.NamespacedName, &provider); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("Reconciling LLMProvider", "name", provider.Name)

	original := provider.DeepCopy()
	provider.Status.ObservedGeneration = provider.Generation

	// Step 1: Check the credential Secret exists.
	secretKey := types.NamespacedName{
		Namespace: provider.Namespace,
		Name:      provider.Spec.CredentialRef.SecretName,
	}
	var secret corev1.Secret
	if err := r.Get(ctx, secretKey, &secret); err != nil {
		if errors.IsNotFound(err) {
			provider.Status.ConnectionVerified = false
			meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: provider.Generation,
				Reason:             "CredentialSecretNotFound",
				Message:            fmt.Sprintf("Secret %q not found", provider.Spec.CredentialRef.SecretName),
			})
			return r.patchStatus(ctx, &provider, original)
		}
		return ctrl.Result{}, err
	}

	// Check the expected key exists in the Secret.
	if _, ok := secret.Data[provider.Spec.CredentialRef.Key]; !ok {
		provider.Status.ConnectionVerified = false
		meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: provider.Generation,
			Reason:             "CredentialKeyNotFound",
			Message: fmt.Sprintf("Key %q not found in Secret %q",
				provider.Spec.CredentialRef.Key, provider.Spec.CredentialRef.SecretName),
		})
		return r.patchStatus(ctx, &provider, original)
	}

	// Step 2: If already verified (or failed) for the current generation,
	// skip re-verification. A spec change (new generation) will re-trigger.
	readyCond := meta.FindStatusCondition(provider.Status.Conditions, ConditionTypeReady)
	if readyCond != nil &&
		readyCond.ObservedGeneration == provider.Generation &&
		(readyCond.Reason == "ConnectionVerified" || readyCond.Reason == "ConnectionFailed") {
		return ctrl.Result{}, nil
	}

	// Step 3: Check for an existing verification Job.
	jobName := verificationJobPrefix + provider.Name
	jobKey := types.NamespacedName{Namespace: provider.Namespace, Name: jobName}
	var job batchv1.Job
	if err := r.Get(ctx, jobKey, &job); err != nil {
		if errors.IsNotFound(err) {
			// No verification Job exists — create one.
			if err := r.createVerificationJob(ctx, &provider, jobName); err != nil {
				logger.Error(err, "Failed to create verification Job")
				return ctrl.Result{}, err
			}
			meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: provider.Generation,
				Reason:             "Verifying",
				Message:            "Connectivity verification in progress",
			})
			return r.patchStatus(ctx, &provider, original)
		}
		return ctrl.Result{}, err
	}

	// Step 3: Check the Job status.
	if isJobComplete(&job) {
		if isJobSucceeded(&job) {
			provider.Status.ConnectionVerified = true
			// Populate discoveredModels from the spec (the Job verified
			// the endpoint is reachable; model discovery from the API
			// response is deferred).
			models := make([]string, len(provider.Spec.Models))
			for i, m := range provider.Spec.Models {
				models[i] = m.Name
			}
			provider.Status.DiscoveredModels = models
			meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: provider.Generation,
				Reason:             "ConnectionVerified",
				Message:            fmt.Sprintf("Endpoint %s is reachable", provider.Spec.Endpoint),
			})
		} else {
			provider.Status.ConnectionVerified = false
			meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: provider.Generation,
				Reason:             "ConnectionFailed",
				Message:            fmt.Sprintf("Verification Job %q failed", jobName),
			})
		}

		// Clean up the completed Job.
		if err := r.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
			logger.Error(err, "Failed to delete verification Job")
		}
	} else {
		// Job still running.
		meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: provider.Generation,
			Reason:             "Verifying",
			Message:            "Connectivity verification in progress",
		})
	}

	return r.patchStatus(ctx, &provider, original)
}

// createVerificationJob creates a Job that verifies connectivity to the
// LLM provider endpoint using the agent base image.
func (r *LLMProviderReconciler) createVerificationJob(
	ctx context.Context,
	provider *konveyoriov1alpha1.LLMProvider,
	jobName string,
) error {
	image := r.VerificationImage
	if image == "" {
		image = DefaultVerificationImage
	}

	// The verification Job runs a simple curl/wget against the endpoint
	// to check reachability. The agent base image includes curl.
	backoffLimit := int32(0)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: provider.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "agentic-controller",
				"app.kubernetes.io/component":  "llm-verification",
				"konveyor.io/llmprovider":      provider.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:  "verify",
							Image: image,
							Command: []string{
								"sh", "-c",
								fmt.Sprintf("wget -q --spider --timeout=10 %s || curl -sf --max-time 10 %s > /dev/null",
									provider.Spec.Endpoint, provider.Spec.Endpoint),
							},
							Env: []corev1.EnvVar{
								{
									Name: "LLM_API_KEY",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: provider.Spec.CredentialRef.SecretName,
											},
											Key: provider.Spec.CredentialRef.Key,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Set owner reference so the Job is cleaned up with the provider.
	if err := ctrl.SetControllerReference(provider, job, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference: %w", err)
	}

	return r.Create(ctx, job)
}

// patchStatus patches the LLMProvider status and returns a reconcile result.
func (r *LLMProviderReconciler) patchStatus(
	ctx context.Context,
	provider *konveyoriov1alpha1.LLMProvider,
	original *konveyoriov1alpha1.LLMProvider,
) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, provider, client.MergeFrom(original)); err != nil {
		log.FromContext(ctx).Error(err, "Failed to patch LLMProvider status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// isJobComplete returns true if the Job has a Complete or Failed condition.
func isJobComplete(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) &&
			c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// isJobSucceeded returns true if the Job has a Complete condition.
func isJobSucceeded(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *LLMProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&konveyoriov1alpha1.LLMProvider{}).
		Owns(&batchv1.Job{}).
		Named("llmprovider").
		Complete(r)
}
