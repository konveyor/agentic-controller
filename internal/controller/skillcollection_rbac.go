package controller

import (
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
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
// legitimate way to run it. That is reported as a message naming the objects to
// create rather than as a failure to retry, because retrying will not fix it.
func (r *SkillCollectionReconciler) ensureEnumeratorRBAC(ctx context.Context, ns string) error {
	labels := map[string]string{
		labelManagedBy: managedByLabel,
		labelComponent: enumeratorRoleName,
	}

	// Labels only when creating, throughout. These are shared, long-lived
	// objects that an admin may already have labelled for their own tooling,
	// and reconciling the grant does not require owning their metadata.
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: r.enumerationServiceAccount(), Namespace: ns}}
	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, sa, func() error {
		if sa.UID == "" {
			sa.Labels = labels
		}
		return nil
	}); err != nil {
		return rbacError(ns, r.enumerationServiceAccount(), err)
	}

	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: enumeratorRoleName, Namespace: ns}}
	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, role, func() error {
		if role.UID == "" {
			role.Labels = labels
		}
		// Rules are reconciled: this is the grant, and it is the one thing
		// here that has to keep saying what the Job may do.
		role.Rules = []rbacv1.PolicyRule{{
			APIGroups: []string{"konveyor.io"},
			Resources: []string{"skillcards"},
			Verbs:     []string{"create", "delete", "get", "list", "update"},
		}}
		return nil
	}); err != nil {
		return rbacError(ns, r.enumerationServiceAccount(), err)
	}

	binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: enumeratorRoleName, Namespace: ns}}
	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, binding, func() error {
		subject := rbacv1.Subject{
			Kind:      kindServiceAccount,
			Name:      r.enumerationServiceAccount(),
			Namespace: ns,
		}
		if binding.UID == "" {
			binding.Labels = labels
			binding.RoleRef = rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     enumeratorRoleName,
			}
			binding.Subjects = []rbacv1.Subject{subject}
			return nil
		}
		// roleRef is immutable. Writing over one that points somewhere else is
		// rejected by the API server on every reconcile, for as long as the
		// binding exists, so name the conflict instead of retrying it. Adding
		// to the subjects rather than replacing them, for the same reason the
		// labels are left alone: this object may not be ours.
		if binding.RoleRef.Kind != "Role" || binding.RoleRef.Name != enumeratorRoleName {
			return fmt.Errorf(
				"RoleBinding %s/%s already binds %s %q, and roleRef cannot be changed; "+
					"delete it, or bind %q to Role %q yourself",
				ns, enumeratorRoleName, binding.RoleRef.Kind, binding.RoleRef.Name,
				subject.Name, enumeratorRoleName)
		}
		if !slices.Contains(binding.Subjects, subject) {
			binding.Subjects = append(binding.Subjects, subject)
		}
		return nil
	}); err != nil {
		return rbacError(ns, r.enumerationServiceAccount(), err)
	}
	return nil
}

// rbacError explains what to do about a namespace the controller may not write
// RBAC into, rather than repeating a Forbidden the user cannot act on.
//
// It names the objects rather than a file to apply. The manifests under
// config/rbac are rendered by kustomize with a name prefix and the operator's
// own namespace, so they can never be the objects this looks for; an operator
// told to apply them would create something the Job never names.
func rbacError(ns, account string, err error) error {
	if errors.IsForbidden(err) {
		return fmt.Errorf(
			"the controller may not create RBAC in namespace %s, so the enumeration Job has no identity. "+
				"Either grant the controller create on serviceaccounts, roles and rolebindings, "+
				"or create in %s: a ServiceAccount named %q, a Role named %q granting "+
				"create,delete,get,list,update on konveyor.io skillcards, and a RoleBinding named %q "+
				"binding the two: %w",
			ns, ns, account, enumeratorRoleName, enumeratorRoleName, err)
	}
	return fmt.Errorf("preparing the enumerator's identity in namespace %s: %w", ns, err)
}
