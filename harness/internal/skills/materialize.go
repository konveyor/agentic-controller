package skills

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
	"github.com/konveyor/migration-harness/internal/logging"
)

// Creating SkillCards from inside the enumerating pod. ADR 0015 argues the
// approach; this notes the two boundaries it crosses.
//
// MODULE BOUNDARY: this file is why the harness depends on the api module,
// controller-runtime and client-go. Nothing else in the binary knows what a
// CustomResource is. Drop this approach and the Kubernetes dependency goes
// with it.
//
// TRUST BOUNDARY: this runs in a pod that mounts a user-supplied image and
// holds a token that can write SkillCards, which name images later mounted
// into agent pods. Two things narrow it: Image comes from the collection and
// never from the walk, and the ServiceAccount is scoped to SkillCards in one
// namespace by config/rbac/skill_enumerator_role.yaml, not by anything here.

// labelSkillCollection marks cards a collection owns, so pruning finds them
// without guessing from names.
const labelSkillCollection = konveyoriov1alpha1.LabelSkillCollection

// Owner identifies the SkillCollection the created cards belong to, so the
// API server garbage collects them with it.
type Owner struct {
	Name      string
	UID       string
	Namespace string
}

// MaterializeOptions describes the cards to write.
type MaterializeOptions struct {
	Owner Owner
	// Image is the source every generated card points at, from the collection
	// rather than the walk. See the trust boundary above.
	Image string
	// Type is the load policy applied to every card.
	Type string
}

// Materialize creates a SkillCard for each skill in dir and deletes any the
// owner previously had that the source no longer contains.
func Materialize(ctx context.Context, dir string, opts MaterializeOptions) ([]string, error) {
	found, err := Validate(dir)
	if err != nil {
		return nil, err
	}

	c, err := newClusterClient()
	if err != nil {
		return nil, err
	}

	skillType := opts.Type
	if skillType == "" {
		skillType = TypeSkill
	}

	owner := metav1.OwnerReference{
		APIVersion:         konveyoriov1alpha1.GroupVersion.String(),
		Kind:               "SkillCollection",
		Name:               opts.Owner.Name,
		UID:                types.UID(opts.Owner.UID),
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}

	wanted := make(map[string]bool, len(found))
	names := make([]string, 0, len(found))

	for _, s := range found {
		name, err := cardName(opts.Owner.Name, s.Name)
		if err != nil {
			return nil, err
		}
		wanted[name] = true
		names = append(names, name)

		card := &konveyoriov1alpha1.SkillCard{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: opts.Owner.Namespace},
		}
		if _, err := ctrl.CreateOrUpdate(ctx, c, card, func() error {
			// The prefix makes a collision unlikely, not impossible, and
			// adopting someone else's card would reparent it into this
			// collection's garbage collection. Refuse instead.
			if held := card.Labels[labelSkillCollection]; card.UID != "" && held != opts.Owner.Name {
				return fmt.Errorf(
					"SkillCard %q already exists and is not owned by collection %q; "+
						"rename the collection or the skill", name, opts.Owner.Name)
			}
			card.Labels = map[string]string{labelSkillCollection: opts.Owner.Name}
			card.OwnerReferences = []metav1.OwnerReference{owner}
			card.Spec.Image = opts.Image
			card.Spec.SubPath = s.SourcePath
			card.Spec.Type = konveyoriov1alpha1.SkillCardType(skillType)
			card.Spec.Description = s.Description
			return nil
		}); err != nil {
			return nil, fmt.Errorf("writing SkillCard %q: %w", name, err)
		}
		logging.Info("wrote SkillCard %s (subPath %q)", name, s.SourcePath)
	}

	var owned konveyoriov1alpha1.SkillCardList
	if err := c.List(ctx, &owned,
		client.InNamespace(opts.Owner.Namespace),
		client.MatchingLabels{labelSkillCollection: opts.Owner.Name}); err != nil {
		return nil, fmt.Errorf("listing owned SkillCards: %w", err)
	}
	for i := range owned.Items {
		if wanted[owned.Items[i].Name] {
			continue
		}
		if err := c.Delete(ctx, &owned.Items[i]); err != nil {
			return nil, fmt.Errorf("pruning SkillCard %q: %w", owned.Items[i].Name, err)
		}
		logging.Info("pruned SkillCard %s, no longer in the source", owned.Items[i].Name)
	}

	return names, nil
}

// cardName scopes a generated card to its collection. Object names stop at 253
// characters, so say which two names ran past it rather than letting the API
// server reject a name the user never typed.
func cardName(owner, skill string) (string, error) {
	name := owner + "-" + skill
	if len(name) > validation.DNS1123SubdomainMaxLength {
		return "", fmt.Errorf(
			"the card for skill %q in collection %q would be named %q, which is %d characters "+
				"and the limit is %d; shorten the collection name",
			skill, owner, name, len(name), validation.DNS1123SubdomainMaxLength)
	}
	return name, nil
}

// newClusterClient builds a client from the pod's projected ServiceAccount.
// A variable so tests can supply a fake without a cluster.
var newClusterClient = func() (client.Client, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("no in-cluster config: %w", err)
	}
	scheme := runtime.NewScheme()
	if err := konveyoriov1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("building the API client: %w", err)
	}
	return c, nil
}
