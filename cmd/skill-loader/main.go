/*
Copyright 2025.

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

// Command skill-loader assembles and validates the skills an agent pod reads.
//
// It ships in the controller's image and runs as the containers the controller
// schedules: an init container on every AgentRun pod, and a Job when a
// SkillCollection names an image. Keeping it out of the agent's image means an
// agent image is not required to carry our binary, and the source list the
// controller writes is always parsed by a loader of the same version.
//
// It is also installable on its own, which is what gives a skill author the
// same answer the loader would give them at pod init, before they publish:
//
//	go install github.com/konveyor/agentic-controller/cmd/skill-loader@latest
//	skill-loader validate ./my-skill
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/konveyor/agentic-controller/internal/skills"
)

const (
	// defaultSrcDir is where the controller stages mounted sources: one
	// subdirectory per SkillCard, ImageVolumes and ConfigMaps alike.
	defaultSrcDir = "/opt/skills-src"

	// defaultDestDir is the assembled root the agent runtime reads.
	defaultDestDir = "/opt/skills"

	// sourcesEnv declares every skill source as a JSON array: the staged ones
	// so an unexpected directory cannot quietly become skills, the git ones
	// because a clone cannot be expressed as a volume, and any load-policy
	// override the SkillCard imposes. Unset falls back to scanning.
	sourcesEnv = "KONVEYOR_SKILL_SOURCES"
)

var rootCmd = &cobra.Command{
	Use:   "skill-loader",
	Short: "Assemble and validate the skills an agent pod reads",
}

var loadCmd = &cobra.Command{
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

var validateCmd = &cobra.Command{
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

var materializeCmd = &cobra.Command{
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
	rootCmd.AddCommand(loadCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(materializeCmd)

	loadCmd.Flags().String("src-dir", defaultSrcDir, "directory holding one subdirectory per staged source")
	loadCmd.Flags().String("dest-dir", defaultDestDir, "assembled skills root the agent runtime reads")
}

// logf writes progress to stderr, which is what `kubectl logs` shows for a
// container that only exists to succeed or fail loudly.
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func main() {
	// The subcommands report their own failures, with the detail that makes an
	// init-container log useful. Printing err here as well would say the same
	// thing twice in the one place an operator looks.
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
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

	if len(sources) == 0 {
		logf("sources: %s (undeclared, scanning), destination: %s", srcDir, destDir)
	} else {
		logf("sources: %d declared under %s, destination: %s", len(sources), srcDir, destDir)
	}

	manifest, err := skills.Load(ctx, skills.Options{
		SrcDir:  srcDir,
		DestDir: destDir,
		Sources: sources,
	})
	if err != nil {
		logf("skill assembly failed: %v", err)
		return err
	}

	if len(manifest.Skills) == 0 {
		logf("no skills staged, the agent will run without any")
		return nil
	}
	for _, s := range manifest.Skills {
		logf("%s (%s) from %s", s.Name, s.Type, s.Source)
	}
	if len(manifest.Rules) > 0 {
		logf("always-loaded rules: %v", manifest.Rules)
	}
	return nil
}

func validateSkills(cmd *cobra.Command, args []string) error {
	found, err := skills.Validate(args[0])
	for _, s := range found {
		logf("%s (%s): %s", s.Name, s.Type, s.Description)
	}
	if err != nil {
		logf("%v", err)
		return err
	}
	logf("%d skill(s) valid", len(found))
	return nil
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

	names, err := skills.Materialize(ctx, args[0], opts)
	if err != nil {
		logf("%v", err)
		return err
	}
	logf("%d SkillCard(s) owned by %s", len(names), opts.Owner.Name)
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
		return nil, fmt.Errorf("expected a JSON array of {name,subPath,type,git:{url,ref}}: %w", err)
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
