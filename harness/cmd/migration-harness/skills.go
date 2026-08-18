package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/konveyor/migration-harness/internal/logging"
	"github.com/konveyor/migration-harness/internal/skills"
)

const (
	// defaultSkillSrcDir is where the controller stages mounted sources: one
	// subdirectory per SkillCard, ImageVolumes and ConfigMaps alike.
	defaultSkillSrcDir = "/opt/skills-src"

	// sourcesEnv declares every skill source as a JSON array: the staged ones
	// so an unexpected directory cannot quietly become skills, the git ones
	// because a clone cannot be expressed as a volume, and any load-policy
	// override the SkillCard imposes. Unset falls back to scanning.
	sourcesEnv = "KONVEYOR_SKILL_SOURCES"
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage the pod's skill directory",
}

var skillsLoadCmd = &cobra.Command{
	Use:   "load",
	Short: "Assemble and validate the skills root from the staged sources",
	Long: `Assemble the skills root that the agent runtime reads.

Runs as an init container, before the agent starts. Each staged source is
either a single skill (SKILL.md at its root) or a bundle (one subdirectory per
skill); the shape is detected, not declared. Every skill is validated and
copied to its frontmatter name under the skills root, so the mount path and
the runtime's view of a skill's name cannot disagree.

Exits non-zero if any skill is unusable, which fails the pod at init rather
than starting an agent whose skills are silently missing.`,
	RunE: loadSkills,
	// Both are logged already, and a failure here is read from kubectl logs of
	// a dead init container. A usage dump buries the one line that says which
	// skill was wrong.
	SilenceUsage:  true,
	SilenceErrors: true,
}

var skillsValidateCmd = &cobra.Command{
	Use:   "validate <dir>",
	Short: "Check that every skill under a directory is usable",
	Long: `Check a skill directory tree without assembling anything.

Accepts the same shapes as load: one skill, or a bundle of them. Intended for
CI, so an unusable skill is caught at review time rather than at pod init.`,
	Args:          cobra.ExactArgs(1),
	RunE:          validateSkills,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	skillsCmd.AddCommand(skillsLoadCmd)
	skillsCmd.AddCommand(skillsValidateCmd)
	rootCmd.AddCommand(skillsCmd)

	skillsLoadCmd.Flags().String("src-dir", defaultSkillSrcDir, "directory holding one subdirectory per staged source")
	// skillsDir() rather than the constant, so the loader and the harness that
	// reads its output cannot be pointed at different directories.
	skillsLoadCmd.Flags().String("dest-dir", skillsDir(), "assembled skills root the agent runtime reads")
}

func loadSkills(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srcDir, err := cmd.Flags().GetString("src-dir")
	if err != nil {
		return err
	}
	destDir, err := cmd.Flags().GetString("dest-dir")
	if err != nil {
		return err
	}

	sources, err := parseSources(os.Getenv(sourcesEnv))
	if err != nil {
		return fmt.Errorf("%s: %w", sourcesEnv, err)
	}

	logging.Header("Skill Assembly")
	if len(sources) == 0 {
		logging.Info("sources: %s (undeclared, scanning), destination: %s", srcDir, destDir)
	} else {
		logging.Info("sources: %d declared under %s, destination: %s", len(sources), srcDir, destDir)
	}

	manifest, err := skills.Load(ctx, skills.Options{
		SrcDir:  srcDir,
		DestDir: destDir,
		Sources: sources,
	})
	if err != nil {
		logging.Err("skill assembly failed: %v", err)
		return err
	}

	if len(manifest.Skills) == 0 {
		logging.Info("no skills staged, the agent will run without any")
		return nil
	}
	for _, s := range manifest.Skills {
		logging.Ok("%s (%s) from %s", s.Name, s.Type, s.Source)
	}
	if len(manifest.Rules) > 0 {
		logging.Info("always-loaded rules: %v", manifest.Rules)
	}
	return nil
}

func validateSkills(cmd *cobra.Command, args []string) error {
	found, err := skills.Validate(args[0])
	for _, s := range found {
		logging.Ok("%s (%s): %s", s.Name, s.Type, s.Description)
	}
	if err != nil {
		logging.Err("%v", err)
		return err
	}
	logging.Ok("%d skill(s) valid", len(found))
	return nil
}

// parseSources reads the declared source list. Empty and unset both mean
// undeclared, which falls back to scanning the staging directory.
func parseSources(raw string) ([]skills.Source, error) {
	if raw == "" {
		return nil, nil
	}
	var out []skills.Source
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("expected a JSON array of {name,type,git:{url,ref,subPath}}: %w", err)
	}
	for i, s := range out {
		if s.Name == "" {
			return nil, fmt.Errorf("entry %d: name is required", i)
		}
		if s.Git != nil && s.Git.URL == "" {
			return nil, fmt.Errorf("entry %d (%s): git.url is required", i, s.Name)
		}
	}
	return out, nil
}
