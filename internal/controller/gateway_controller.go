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
	verificationJobPrefix = "gw-verify-"

	// DefaultVerificationImage is the default image used for Gateway
	// verification when no override is configured. In production, the
	// controller should use the agentic-controller-agent image from
	// this repository.
	DefaultVerificationImage = "quay.io/konveyor/agentic-controller-agent:latest"

	// verificationHTTPCodePattern requires a 2xx status from the probe.
	verificationHTTPCodePattern = "^2"
)

// GatewayReconciler reconciles a Gateway object.
type GatewayReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// VerificationImage overrides the container image used for
	// connectivity verification Jobs. Defaults to DefaultVerificationImage.
	VerificationImage string
}

// +kubebuilder:rbac:groups=konveyor.io,resources=gateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=konveyor.io,resources=gateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=konveyor.io,resources=gateways/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile handles Gateway reconciliation.
//
// The controller verifies gateway connectivity by:
//  1. Checking that the referenced credential Secret exists
//  2. Creating a verification Job that tests the endpoint using the
//     agent base image
//  3. Updating status based on the Job result
func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var gateway konveyoriov1alpha1.Gateway
	if err := r.Get(ctx, req.NamespacedName, &gateway); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("Reconciling Gateway", "name", gateway.Name)

	original := gateway.DeepCopy()
	gateway.Status.ObservedGeneration = gateway.Generation

	// Step 1: Check the credential Secret exists.
	secretKey := types.NamespacedName{
		Namespace: gateway.Namespace,
		Name:      gateway.Spec.CredentialRef.SecretName,
	}
	var secret corev1.Secret
	if err := r.Get(ctx, secretKey, &secret); err != nil {
		if errors.IsNotFound(err) {
			gateway.Status.ConnectionVerified = false
			meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: gateway.Generation,
				Reason:             "CredentialSecretNotFound",
				Message:            fmt.Sprintf("Secret %q not found", gateway.Spec.CredentialRef.SecretName),
			})
			return r.patchStatus(ctx, &gateway, original)
		}
		return ctrl.Result{}, err
	}

	// Check the expected key exists in the Secret. A keyless credentialRef
	// means the whole Secret is the credential (multi-variable, e.g. AWS
	// SigV4) — then it just must not be empty.
	if gateway.Spec.CredentialRef.Key != "" {
		if _, ok := secret.Data[gateway.Spec.CredentialRef.Key]; !ok {
			gateway.Status.ConnectionVerified = false
			meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: gateway.Generation,
				Reason:             "CredentialKeyNotFound",
				Message: fmt.Sprintf("Key %q not found in Secret %q",
					gateway.Spec.CredentialRef.Key, gateway.Spec.CredentialRef.SecretName),
			})
			return r.patchStatus(ctx, &gateway, original)
		}
	} else if len(secret.Data) == 0 {
		gateway.Status.ConnectionVerified = false
		meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: gateway.Generation,
			Reason:             "CredentialSecretEmpty",
			Message: fmt.Sprintf("Secret %q has no data keys",
				gateway.Spec.CredentialRef.SecretName),
		})
		return r.patchStatus(ctx, &gateway, original)
	}

	// Step 2: If already verified (or failed) for the current generation,
	// skip re-verification. A spec change (new generation) will re-trigger.
	readyCond := meta.FindStatusCondition(gateway.Status.Conditions, ConditionTypeReady)
	if readyCond != nil &&
		readyCond.ObservedGeneration == gateway.Generation &&
		(readyCond.Reason == "ConnectionVerified" || readyCond.Reason == "ConnectionFailed") {
		return ctrl.Result{}, nil
	}

	// Clean up verification Jobs from prior generations. If a Gateway
	// spec changes while verification is queued/running, the old Job
	// is orphaned because completion events reconcile the new generation.
	var oldJobs batchv1.JobList
	if err := r.List(ctx, &oldJobs,
		client.InNamespace(gateway.Namespace),
		client.MatchingLabels{"konveyor.io/gateway": gateway.Name},
	); err != nil {
		return ctrl.Result{}, err
	}
	currentJobName := fmt.Sprintf("%s%s-gen%d", verificationJobPrefix, gateway.Name, gateway.Generation)
	for i := range oldJobs.Items {
		if oldJobs.Items[i].Name != currentJobName {
			if err := r.Delete(ctx, &oldJobs.Items[i],
				client.PropagationPolicy(metav1.DeletePropagationBackground),
			); client.IgnoreNotFound(err) != nil {
				logger.V(1).Info("Failed to delete stale verification Job",
					"job", oldJobs.Items[i].Name)
			}
		}
	}

	// Step 3: Check for an existing verification Job.
	// Include generation in name to avoid collisions when re-verifying.
	jobName := currentJobName
	jobKey := types.NamespacedName{Namespace: gateway.Namespace, Name: jobName}
	var job batchv1.Job
	if err := r.Get(ctx, jobKey, &job); err != nil {
		if errors.IsNotFound(err) {
			// No verification Job exists — create one.
			if err := r.createVerificationJob(ctx, &gateway, jobName); err != nil {
				logger.Error(err, "Failed to create verification Job")
				return ctrl.Result{}, err
			}
			gateway.Status.ConnectionVerified = false
			meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: gateway.Generation,
				Reason:             "Verifying",
				Message:            "Connectivity verification in progress",
			})
			return r.patchStatus(ctx, &gateway, original)
		}
		return ctrl.Result{}, err
	}

	// Step 3: Check the Job status.
	if isJobComplete(&job) {
		if isJobSucceeded(&job) {
			gateway.Status.ConnectionVerified = true
			meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: gateway.Generation,
				Reason:             "ConnectionVerified",
				Message:            fmt.Sprintf("Endpoint %s is reachable", gateway.Spec.Endpoint),
			})
		} else {
			gateway.Status.ConnectionVerified = false
			meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: gateway.Generation,
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
		gateway.Status.ConnectionVerified = false
		meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: gateway.Generation,
			Reason:             "Verifying",
			Message:            "Connectivity verification in progress",
		})
	}

	return r.patchStatus(ctx, &gateway, original)
}

// gatewayVerificationCurlCommand builds the shell command used by the
// verification Job. When includeAuth is true, the request sends
// Authorization: Bearer $LLM_API_KEY. Keyless credentials omit the header
// so gateways are probed for reachability without an empty Bearer token.
func gatewayVerificationCurlCommand(includeAuth bool) string {
	curl := "curl -sk --max-time 10 -o /dev/null -w '%{http_code}'"
	if includeAuth {
		curl += ` -H "Authorization: Bearer $LLM_API_KEY"`
	}
	return curl + ` "$LLM_ENDPOINT/v1/models" | grep -qE '` + verificationHTTPCodePattern + `'`
}

// createVerificationJob creates a Job that verifies connectivity to the
// gateway endpoint using the agent base image.
func (r *GatewayReconciler) createVerificationJob(
	ctx context.Context,
	gateway *konveyoriov1alpha1.Gateway,
	jobName string,
) error {
	image := r.VerificationImage
	if image == "" {
		image = DefaultVerificationImage
	}

	// The verification Job runs a simple curl against the endpoint.
	// The agent base image includes curl. Only 2xx counts as success so
	// 401/403 (invalid or missing API key) fail verification instead of
	// marking ConnectionVerified. Keyless credentialRef (empty key,
	// e.g. AWS SigV4) omits Authorization entirely — an empty Bearer
	// would 401 under the ^2 check.
	includeAuth := gateway.Spec.CredentialRef.Key != ""
	env := []corev1.EnvVar{{Name: "LLM_ENDPOINT", Value: gateway.Spec.Endpoint}}
	if includeAuth {
		env = append(env, corev1.EnvVar{
			Name: "LLM_API_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: gateway.Spec.CredentialRef.SecretName,
					},
					Key: gateway.Spec.CredentialRef.Key,
				},
			},
		})
	}

	backoffLimit := int32(0)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: gateway.Namespace,
			Labels: map[string]string{
				labelManagedBy:        managedByLabel,
				labelComponent:        "gateway-verification",
				"konveyor.io/gateway": gateway.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:            "verify",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command: []string{
								"sh", "-c",
								// Use env vars to avoid shell injection.
								gatewayVerificationCurlCommand(includeAuth),
							},
							Env: env,
						},
					},
				},
			},
		},
	}

	// Set owner reference so the Job is cleaned up with the gateway.
	if err := ctrl.SetControllerReference(gateway, job, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference: %w", err)
	}

	return r.Create(ctx, job)
}

// patchStatus patches the Gateway status and returns a reconcile result.
func (r *GatewayReconciler) patchStatus(
	ctx context.Context,
	gateway *konveyoriov1alpha1.Gateway,
	original *konveyoriov1alpha1.Gateway,
) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, gateway, client.MergeFrom(original)); err != nil {
		log.FromContext(ctx).Error(err, "Failed to patch Gateway status")
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
func (r *GatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&konveyoriov1alpha1.Gateway{}).
		Owns(&batchv1.Job{}).
		Named("gateway").
		Complete(r)
}
