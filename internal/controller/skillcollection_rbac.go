package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The enumeration Job runs in the collection's namespace, and a ServiceAccount
// only exists in the namespace it was created in. Shipping one in the
// operator's namespace therefore only serves collections that happen to live
// there, and everything else waits on a pod that will never be admitted.
//
// So the controller provisions the identity where it is needed. Kubernetes
// forbids granting permissions the granter does not hold, and this controller
// holds SkillCards cluster-wide, so what it can create here is bounded to what
// the enumerator already had: SkillCards, in one namespace.
//
// These are deliberately not owned by the collection. Several collections in a
// namespace share one identity, and garbage collecting it with whichever was
// deleted first would break the others.

const (
	enumeratorRoleName = "skill-enumerator"

	labelComponent     = "app.kubernetes.io/component"
	kindServiceAccount = "ServiceAccount"
)

// ensureEnumeratorRBAC makes the enumerator's identity exist in ns.
//
// A cluster may refuse the controller permission to write RBAC, which is a
// legitimate way to run it. That is reported as a message naming the manifests
// to apply rather than as a failure to retry, because retrying will not fix it.
func (r *SkillCollectionReconciler) ensureEnumeratorRBAC(ctx context.Context, ns string) error {
	labels := map[string]string{
		"app.kubernetes.io/managed-by": managedByLabel,
		labelComponent:                 enumeratorRoleName,
	}

	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: r.enumerationServiceAccount(), Namespace: ns}}
	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, sa, func() error {
		sa.Labels = labels
		return nil
	}); err != nil {
		return rbacError(ns, err)
	}

	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: enumeratorRoleName, Namespace: ns}}
	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Labels = labels
		role.Rules = []rbacv1.PolicyRule{{
			APIGroups: []string{"konveyor.io"},
			Resources: []string{"skillcards"},
			Verbs:     []string{"create", "delete", "get", "list", "update"},
		}}
		return nil
	}); err != nil {
		return rbacError(ns, err)
	}

	binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: enumeratorRoleName, Namespace: ns}}
	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, binding, func() error {
		binding.Labels = labels
		binding.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     enumeratorRoleName,
		}
		binding.Subjects = []rbacv1.Subject{{
			Kind:      kindServiceAccount,
			Name:      r.enumerationServiceAccount(),
			Namespace: ns,
		}}
		return nil
	}); err != nil {
		return rbacError(ns, err)
	}
	return nil
}

// rbacError explains what to do about a namespace the controller may not write
// RBAC into, rather than repeating a Forbidden the user cannot act on.
func rbacError(ns string, err error) error {
	if errors.IsForbidden(err) {
		return fmt.Errorf(
			"the controller may not create RBAC in namespace %s, so the enumeration Job has no identity. "+
				"Apply config/rbac/skill_enumerator_{service_account,role,role_binding}.yaml to %s, "+
				"or grant the controller create on serviceaccounts, roles and rolebindings: %w", ns, ns, err)
	}
	return fmt.Errorf("preparing the enumerator's identity in namespace %s: %w", ns, err)
}

// enumeratorRBACReady reports whether the identity the Job names actually
// exists, so a missing one is a message on the collection rather than a pod
// stuck pending admission with nothing to read.
func (r *SkillCollectionReconciler) enumeratorRBACReady(ctx context.Context, ns string) error {
	var sa corev1.ServiceAccount
	key := client.ObjectKey{Namespace: ns, Name: r.enumerationServiceAccount()}
	if err := r.Get(ctx, key, &sa); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf(
				"ServiceAccount %s/%s does not exist, so the enumeration Job cannot be admitted. "+
					"Apply config/rbac/skill_enumerator_{service_account,role,role_binding}.yaml to %s",
				ns, r.enumerationServiceAccount(), ns)
		}
		return fmt.Errorf("reading ServiceAccount %s/%s: %w", ns, r.enumerationServiceAccount(), err)
	}
	return nil
}
