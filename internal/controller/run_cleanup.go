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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	agentRunKind         = "AgentRun"
	agentWorkflowRunKind = "AgentWorkflowRun"
	sandboxKind          = "Sandbox"
)

// isOwnerlessManagedRunResource identifies children created directly by the
// AgentRun controller whose controller owner reference is absent. Owned
// children use the builder's ordinary Owns watch; requiring both labels keeps
// this recovery watch from treating an unrelated object as controller-owned.
func isOwnerlessManagedRunResource(obj client.Object) bool {
	labels := obj.GetLabels()
	return labels[labelManagedBy] == managedByLabel && labels[labelAgentRun] != "" && !hasControllerOwner(obj)
}

// isOwnerlessManagedWorkflowRunResource identifies stage AgentRuns created by
// the AgentWorkflowRun controller whose controller owner reference is absent.
func isOwnerlessManagedWorkflowRunResource(obj client.Object) bool {
	labels := obj.GetLabels()
	return labels[labelManagedBy] == managedByLabel && labels[labelAgentWorkflowRun] != "" && !hasControllerOwner(obj)
}

func hasControllerOwner(obj client.Object) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Controller != nil && *ref.Controller {
			return true
		}
	}
	return false
}

// workflowRunForLabeledResource maps a stage AgentRun to its workflow run by
// label even when the controller owner reference is absent.
func workflowRunForLabeledResource(_ context.Context, obj client.Object) []reconcile.Request {
	name := obj.GetLabels()[labelAgentWorkflowRun]
	if name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      name,
	}}}
}

// mayDeleteForMissingOwner permits deletion when the object is ownerless or
// its controller owner is the missing parent being reconciled. A different
// controller owner wins: labels are useful recovery metadata, but they never
// authorize stealing or deleting another controller's live object.
func mayDeleteForMissingOwner(obj client.Object, apiVersion, kind, name string) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Controller == nil || !*ref.Controller {
			continue
		}
		return ref.APIVersion == apiVersion && ref.Kind == kind && ref.Name == name
	}
	return true
}

func deleteOrphanedObject(
	ctx context.Context,
	c client.Client,
	obj client.Object,
	resourceKind string,
	ownerAPIVersion string,
	ownerKind string,
	ownerName string,
) error {
	if !mayDeleteForMissingOwner(obj, ownerAPIVersion, ownerKind, ownerName) {
		log.FromContext(ctx).V(1).Info("Skipping labeled resource owned by another controller",
			"kind", resourceKind, "namespace", obj.GetNamespace(), "name", obj.GetName(),
			"missingOwnerKind", ownerKind, "missingOwnerName", ownerName)
		return nil
	}

	log.FromContext(ctx).Info("Deleting orphaned resource for missing run",
		"kind", resourceKind, "namespace", obj.GetNamespace(), "name", obj.GetName(),
		"missingOwnerKind", ownerKind, "missingOwnerName", ownerName)
	if err := c.Delete(ctx, obj); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("deleting orphaned %s %s/%s: %w",
			resourceKind, obj.GetNamespace(), obj.GetName(), err)
	}
	return nil
}

// mayDeletePodForMissingRun follows a Pod's Sandbox controller reference before
// allowing the AgentRun cleanup path to delete it. A live matching Sandbox
// remains authoritative; deleting an eligible Sandbox will either cascade to
// the Pod or enqueue another sweep after the Sandbox disappears.
func (r *AgentRunReconciler) mayDeletePodForMissingRun(
	ctx context.Context,
	pod *corev1.Pod,
	runName string,
) (bool, error) {
	for _, ref := range pod.GetOwnerReferences() {
		if ref.Controller == nil || !*ref.Controller {
			continue
		}
		if ref.APIVersion != sandboxv1beta1.GroupVersion.String() ||
			ref.Kind != sandboxKind || ref.Name != runName {
			return false, nil
		}

		var reader client.Reader = r.Client
		if r.apiReader != nil {
			reader = r.apiReader
		}
		var sandbox sandboxv1beta1.Sandbox
		key := types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}
		if err := reader.Get(ctx, key, &sandbox); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, fmt.Errorf("getting Sandbox owner for Pod %s/%s: %w",
				pod.Namespace, pod.Name, err)
		}

		// A same-name Sandbox with a different UID is not this Pod's owner. The
		// referenced Sandbox is gone, so the Pod is safe to sweep.
		if ref.UID != "" && sandbox.UID != "" && ref.UID != sandbox.UID {
			return true, nil
		}
		return false, nil
	}
	return true, nil
}

// cleanupOrphanedRunResources is the fallback behind Kubernetes owner-based
// garbage collection. It removes controller-managed children that still carry
// the AgentRun label after that run no longer exists. Deleting the Sandbox also
// cascades through Agent Sandbox to its Service and other owned objects; Pods
// are included directly for recovery from a broken Sandbox-to-Pod owner link.
func (r *AgentRunReconciler) cleanupOrphanedRunResources(
	ctx context.Context,
	key types.NamespacedName,
) error {
	selector := client.MatchingLabels{
		labelManagedBy: managedByLabel,
		labelAgentRun:  key.Name,
	}
	ownerAPIVersion := konveyoriov1alpha1.GroupVersion.String()

	var sandboxes sandboxv1beta1.SandboxList
	if err := r.List(ctx, &sandboxes, client.InNamespace(key.Namespace), selector); err != nil {
		return fmt.Errorf("listing Sandboxes for missing AgentRun %s: %w", key, err)
	}
	for i := range sandboxes.Items {
		if err := deleteOrphanedObject(ctx, r.Client, &sandboxes.Items[i], sandboxKind,
			ownerAPIVersion, agentRunKind, key.Name); err != nil {
			return err
		}
	}

	var configMaps corev1.ConfigMapList
	if err := r.List(ctx, &configMaps, client.InNamespace(key.Namespace), selector); err != nil {
		return fmt.Errorf("listing ConfigMaps for missing AgentRun %s: %w", key, err)
	}
	for i := range configMaps.Items {
		if err := deleteOrphanedObject(ctx, r.Client, &configMaps.Items[i], "ConfigMap",
			ownerAPIVersion, agentRunKind, key.Name); err != nil {
			return err
		}
	}

	var secrets corev1.SecretList
	if err := r.List(ctx, &secrets, client.InNamespace(key.Namespace), selector); err != nil {
		return fmt.Errorf("listing Secrets for missing AgentRun %s: %w", key, err)
	}
	for i := range secrets.Items {
		if err := deleteOrphanedObject(ctx, r.Client, &secrets.Items[i], "Secret",
			ownerAPIVersion, agentRunKind, key.Name); err != nil {
			return err
		}
	}

	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(key.Namespace), selector); err != nil {
		return fmt.Errorf("listing Pods for missing AgentRun %s: %w", key, err)
	}
	for i := range pods.Items {
		mayDelete, err := r.mayDeletePodForMissingRun(ctx, &pods.Items[i], key.Name)
		if err != nil {
			return err
		}
		if !mayDelete {
			log.FromContext(ctx).V(1).Info("Skipping labeled Pod with a foreign ownership chain",
				"namespace", pods.Items[i].Namespace, "name", pods.Items[i].Name,
				"missingOwnerKind", agentRunKind, "missingOwnerName", key.Name)
			continue
		}
		if err := deleteOrphanedObject(ctx, r.Client, &pods.Items[i], "Pod",
			sandboxv1beta1.GroupVersion.String(), sandboxKind, key.Name); err != nil {
			return err
		}
	}

	return nil
}

// cleanupOrphanedWorkflowRunResources removes stage AgentRuns left behind
// after their AgentWorkflowRun is gone. Each AgentRun deletion then cascades to
// its Sandbox, ConfigMaps, Secrets, Pod, and Sandbox-owned Service.
func (r *AgentWorkflowRunReconciler) cleanupOrphanedWorkflowRunResources(
	ctx context.Context,
	key types.NamespacedName,
) error {
	var runs konveyoriov1alpha1.AgentRunList
	if err := r.List(ctx, &runs,
		client.InNamespace(key.Namespace),
		client.MatchingLabels{
			labelManagedBy:        managedByLabel,
			labelAgentWorkflowRun: key.Name,
		},
	); err != nil {
		return fmt.Errorf("listing AgentRuns for missing AgentWorkflowRun %s: %w", key, err)
	}

	ownerAPIVersion := konveyoriov1alpha1.GroupVersion.String()
	for i := range runs.Items {
		if err := deleteOrphanedObject(ctx, r.Client, &runs.Items[i], agentRunKind,
			ownerAPIVersion, agentWorkflowRunKind, key.Name); err != nil {
			return err
		}
	}
	return nil
}
