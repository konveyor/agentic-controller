package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/konveyor/migration-harness/internal/logging"
	"github.com/konveyor/migration-harness/internal/skills"
)

// The materialize subcommand is what makes this binary depend on the api
// module and controller-runtime; see internal/skills/materialize.go.

var skillsMaterializeCmd = &cobra.Command{
	Use:   "materialize <dir>",
	Short: "Create a SkillCard for each skill a source contains",
	Long: `Write a SkillCard per skill in the source, owned by a SkillCollection.

Runs as a short-lived Job that mounts the source the way the agent pod does,
so the controller needs no registry client of its own. Writing the cards from
here rather than reporting them back removes the payload channel, at the cost
of a ServiceAccount on a pod that mounts user-supplied content.

The collection is named by KONVEYOR_COLLECTION_NAME, KONVEYOR_COLLECTION_UID
and KONVEYOR_NAMESPACE; the image every card points at by KONVEYOR_SKILL_IMAGE,
which comes from the collection rather than from the source.`,
	Args:          cobra.ExactArgs(1),
	RunE:          materializeSkills,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	skillsCmd.AddCommand(skillsMaterializeCmd)
}

func materializeSkills(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	opts := skills.MaterializeOptions{
		Owner: skills.Owner{
			Name:      os.Getenv("KONVEYOR_COLLECTION_NAME"),
			UID:       os.Getenv("KONVEYOR_COLLECTION_UID"),
			Namespace: os.Getenv("KONVEYOR_NAMESPACE"),
		},
		Image: os.Getenv("KONVEYOR_SKILL_IMAGE"),
		Type:  os.Getenv("KONVEYOR_SKILL_TYPE"),
	}
	for k, v := range map[string]string{
		"KONVEYOR_COLLECTION_NAME": opts.Owner.Name,
		"KONVEYOR_COLLECTION_UID":  opts.Owner.UID,
		"KONVEYOR_NAMESPACE":       opts.Owner.Namespace,
		"KONVEYOR_SKILL_IMAGE":     opts.Image,
	} {
		if v == "" {
			return fmt.Errorf("%s is required", k)
		}
	}

	logging.Header("Skill Materialization")
	names, err := skills.Materialize(ctx, args[0], opts)
	if err != nil {
		logging.Err("%v", err)
		return err
	}
	logging.Ok("%d SkillCard(s) owned by %s", len(names), opts.Owner.Name)
	return nil
}
