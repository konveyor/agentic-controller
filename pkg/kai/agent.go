package kai

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	agenticv1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// Agent parameter type values (mirrors agenticv1alpha1.ParamType).
const (
	paramTypeString  = "string"
	paramTypeNumber  = "number"
	paramTypeBoolean = "boolean"
)

func newAgentCommand(cfg *kaiConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "agent",
		Aliases: []string{"ag", "agents"},
		Short:   "Manage Agents",
	}
	cmd.AddCommand(newAgentCreateCommand(cfg))
	cmd.AddCommand(newAgentEditCommand(cfg))
	cmd.AddCommand(newAgentDeleteCommand(cfg))
	cmd.AddCommand(newAgentListCommand(cfg))
	cmd.AddCommand(newAgentRunCommand(cfg))
	cmd.AddCommand(newAgentGetCommand(cfg))
	cmd.AddCommand(newAgentDescribeCommand(cfg))
	cmd.AddCommand(newAgentRunsCommand(cfg))
	return cmd
}

func newAgentCreateCommand(cfg *kaiConfig) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   useCreate,
		Short: "Create an Agent via an interactive wizard",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			return runAgentCreate(cmd.Context(), cfg, name, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the Agent YAML without creating it")
	return cmd
}

func runAgentCreate(ctx context.Context, cfg *kaiConfig, name string, dryRun bool) error {
	if err := requireTerminal(); err != nil {
		return err
	}
	cl, err := cfg.newClient()
	if err != nil {
		return err
	}

	gateways, err := gatewayNames(ctx, cl, cfg.namespace)
	if err != nil {
		return fmt.Errorf("failed to list gateways: %w", err)
	}
	if len(gateways) == 0 {
		return fmt.Errorf("no gateways found in namespace %q; create one first with 'kubectl kai gateway create'",
			cfg.namespace)
	}
	cards, err := skillCardNames(ctx, cl, cfg.namespace)
	if err != nil {
		return err
	}
	collections, err := skillCollectionNames(ctx, cl, cfg.namespace)
	if err != nil {
		return err
	}

	nameVal := name
	image := ""
	var selectedGateways []string
	prompt := ""

	fields := []huh.Field{}
	if name == "" {
		fields = append(fields, inputField("Agent name", "my-agent", &nameVal, requiredValidator("name")))
	}
	fields = append(fields,
		inputField("Container image", "quay.io/konveyor/my-agent:latest", &image, requiredValidator(sourceImage)),
		huh.NewMultiSelect[string]().
			Title("Gateways (select at least one)").
			Options(huh.NewOptions(gateways...)...).
			Validate(func(s []string) error {
				if len(s) == 0 {
					return fmt.Errorf("select at least one gateway")
				}
				return nil
			}).
			Value(&selectedGateways),
		huh.NewText().Title("System prompt (optional)").Value(&prompt),
	)
	if err := runForm(fields...); err != nil {
		return err
	}
	name = strings.TrimSpace(nameVal)

	var selectedCards, selectedCollections []string
	if len(cards) > 0 {
		if err := runForm(multiSelectField("Skill cards (optional)", cards, &selectedCards)); err != nil {
			return err
		}
	}
	if len(collections) > 0 {
		if err := runForm(multiSelectField("Skill collections (optional)", collections, &selectedCollections)); err != nil {
			return err
		}
	}

	params, err := collectParams()
	if err != nil {
		return err
	}

	agent := &agenticv1alpha1.Agent{
		TypeMeta:   metav1.TypeMeta{APIVersion: agenticv1alpha1.GroupVersion.String(), Kind: "Agent"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cfg.namespace},
		Spec: agenticv1alpha1.AgentSpec{
			Image:  strings.TrimSpace(image),
			Prompt: strings.TrimSpace(prompt),
			Params: params,
		},
	}
	for _, g := range selectedGateways {
		agent.Spec.Gateways = append(agent.Spec.Gateways, agenticv1alpha1.AgentGatewayRef{Ref: g})
	}
	for _, c := range selectedCards {
		agent.Spec.SkillCards = append(agent.Spec.SkillCards, agenticv1alpha1.AgentSkillCardRef{Ref: c})
	}
	for _, c := range selectedCollections {
		agent.Spec.SkillCollections = append(agent.Spec.SkillCollections, agenticv1alpha1.AgentSkillCollectionRef{Ref: c})
	}

	return previewAndCreate(ctx, cl, agent, name, "agent", cfg.namespace, dryRun)
}

// collectParams interactively gathers Agent parameter declarations.
func collectParams() ([]agenticv1alpha1.Param, error) {
	var params []agenticv1alpha1.Param
	for {
		add, err := confirm("Add a parameter?", false)
		if err != nil {
			return nil, err
		}
		if !add {
			return params, nil
		}
		var (
			pName string
			pType = paramTypeString
			pDesc string
			pDef  string
			pReq  bool
		)
		if err := runForm(
			inputField("Parameter name", "TARGET_BRANCH", &pName, requiredValidator("parameter name")),
			selectField("Type", []string{paramTypeString, paramTypeNumber, paramTypeBoolean}, &pType),
			inputField("Description (optional)", "", &pDesc, nil),
			inputField("Default value (optional)", "", &pDef, nil),
			huh.NewConfirm().Title("Required?").Value(&pReq),
		); err != nil {
			return nil, err
		}
		// A parameter with a default cannot also be required (CRD rule).
		if pReq && strings.TrimSpace(pDef) != "" {
			fmt.Fprintln(os.Stderr, "note: a parameter with a default cannot be required; marking it optional")
			pReq = false
		}
		params = append(params, agenticv1alpha1.Param{
			Name:        strings.TrimSpace(pName),
			Type:        agenticv1alpha1.ParamType(pType),
			Description: strings.TrimSpace(pDesc),
			Default:     strings.TrimSpace(pDef),
			Required:    pReq,
		})
	}
}

func newAgentEditCommand(cfg *kaiConfig) *cobra.Command {
	return &cobra.Command{
		Use:   useEdit,
		Short: "Edit an Agent in your $EDITOR",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := cfg.newClient()
			if err != nil {
				return err
			}
			agent := &agenticv1alpha1.Agent{ObjectMeta: metav1.ObjectMeta{Name: args[0], Namespace: cfg.namespace}}
			return editResource(cmd.Context(), cl, agent)
		},
	}
}

func newAgentDeleteCommand(cfg *kaiConfig) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   useDelete,
		Short: "Delete an Agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := cfg.newClient()
			if err != nil {
				return err
			}
			agent := &agenticv1alpha1.Agent{ObjectMeta: metav1.ObjectMeta{Name: args[0], Namespace: cfg.namespace}}
			return deleteResource(cmd.Context(), cl, agent, args[0], "agent", yes)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newAgentListCommand(cfg *kaiConfig) *cobra.Command {
	return &cobra.Command{
		Use:   useList,
		Short: "List Agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := cfg.newClient()
			if err != nil {
				return err
			}
			var list agenticv1alpha1.AgentList
			if err := cl.List(cmd.Context(), &list, client.InNamespace(cfg.namespace)); err != nil {
				return fmt.Errorf("failed to list agents: %w", err)
			}
			if len(list.Items) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "no agents found in namespace %q\n", cfg.namespace)
				return nil
			}
			rows := make([][]string, 0, len(list.Items))
			for i := range list.Items {
				a := &list.Items[i]
				gws := make([]string, 0, len(a.Spec.Gateways))
				for _, g := range a.Spec.Gateways {
					gws = append(gws, g.Ref)
				}
				rows = append(rows, []string{
					a.Name,
					a.Spec.Image,
					strings.Join(gws, ","),
					readyStatus(a.Status.Conditions),
					age(a.CreationTimestamp),
				})
			}
			table(cmd.OutOrStdout(), []string{colName, "IMAGE", "GATEWAYS", colReady, colAge}, rows)
			return nil
		},
	}
}

func newAgentRunCommand(cfg *kaiConfig) *cobra.Command {
	var (
		gateway      string
		instructions string
		paramFlags   []string
		wait         bool
		rc           runContext
	)
	cmd := &cobra.Command{
		Use:   "run <agent-name>",
		Short: "Run an Agent (interactive when parameters or a gateway choice are needed)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rc.resolveHubToken(); err != nil {
				return err
			}
			env, envFrom, err := rc.build(cmd.Flags().Changed("git-secret"))
			if err != nil {
				return err
			}
			return runAgentRun(cmd.Context(), cfg, args[0], gateway, instructions, paramFlags, wait, env, envFrom)
		},
	}
	cmd.Flags().StringVar(&gateway, "gateway", "", "gateway to use (must be one of the Agent's gateways)")
	cmd.Flags().StringVar(&instructions, "instructions", "", "task-specific instructions (additional prompt)")
	cmd.Flags().StringArrayVar(&paramFlags, "param", nil, "parameter value as key=value (repeatable)")
	cmd.Flags().BoolVar(&wait, "wait", false, "wait for the run to reach a terminal phase")
	rc.addFlags(cmd)
	return cmd
}

func runAgentRun(
	ctx context.Context, cfg *kaiConfig, name, gateway, instructions string,
	paramFlags []string, wait bool, env []corev1.EnvVar, envFrom []corev1.EnvFromSource,
) error {
	cl, err := cfg.newClient()
	if err != nil {
		return err
	}

	agent := &agenticv1alpha1.Agent{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: cfg.namespace, Name: name}, agent); err != nil {
		return fmt.Errorf("failed to get agent %q: %w", name, err)
	}

	gwOptions := make([]string, 0, len(agent.Spec.Gateways))
	for _, g := range agent.Spec.Gateways {
		gwOptions = append(gwOptions, g.Ref)
	}

	// Resolve the gateway: honor the flag, else pick when unambiguous, else prompt.
	if gateway != "" {
		if !contains(gwOptions, gateway) {
			return fmt.Errorf("gateway %q is not one of the agent's gateways: %s", gateway, strings.Join(gwOptions, ", "))
		}
	} else if len(gwOptions) == 1 {
		gateway = gwOptions[0]
	} else if len(gwOptions) > 1 {
		if err := requireTerminal(); err != nil {
			return fmt.Errorf("agent has multiple gateways; specify one with --gateway: %s", strings.Join(gwOptions, ", "))
		}
		if err := runForm(selectField("Gateway", gwOptions, &gateway)); err != nil {
			return err
		}
	}

	// Resolve parameter values.
	provided, err := parseParamFlags(paramFlags)
	if err != nil {
		return err
	}
	runParams, err := resolveRunParams(agent.Spec.Params, provided)
	if err != nil {
		return err
	}

	// Optional additional instructions.
	if instructions == "" && isInteractive() {
		if err := runForm(huh.NewText().Title("Additional instructions (optional)").Value(&instructions)); err != nil {
			return err
		}
	}

	run := &agenticv1alpha1.AgentRun{
		TypeMeta:   metav1.TypeMeta{APIVersion: agenticv1alpha1.GroupVersion.String(), Kind: "AgentRun"},
		ObjectMeta: metav1.ObjectMeta{GenerateName: name + "-", Namespace: cfg.namespace},
		Spec: agenticv1alpha1.AgentRunSpec{
			AgentRef:     name,
			Gateway:      gateway,
			Params:       runParams,
			Instructions: strings.TrimSpace(instructions),
			Env:          env,
			EnvFrom:      envFrom,
		},
	}
	if err := cl.Create(ctx, run); err != nil {
		return fmt.Errorf("failed to create AgentRun: %w", err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "agent run %q created\n", run.Name)

	if wait {
		_, _ = fmt.Fprintln(os.Stdout, "waiting for run to complete...")
		return waitForRun(ctx, cl, run, func() agenticv1alpha1.AgentRunPhase { return run.Status.Phase })
	}
	return nil
}

// resolveRunParams builds AgentRun params for each declared Agent param, using
// provided values, prompting interactively when missing, and enforcing that
// required params get a value.
func resolveRunParams(
	declared []agenticv1alpha1.Param, provided map[string]string,
) ([]agenticv1alpha1.ParamValue, error) {
	var out []agenticv1alpha1.ParamValue
	for _, p := range declared {
		value, ok := provided[p.Name]
		if !ok {
			if !isInteractive() {
				if p.Required {
					return nil, fmt.Errorf("missing required parameter %q (provide with --param %s=<value>)", p.Name, p.Name)
				}
				value = p.Default
			} else {
				value = p.Default
				title := fmt.Sprintf("Parameter %q (%s)", p.Name, p.Type)
				if p.Description != "" {
					title = fmt.Sprintf("%s — %s", title, p.Description)
				}
				var validate func(string) error
				if p.Required {
					validate = requiredValidator(p.Name)
				}
				if err := runForm(inputField(title, p.Default, &value, validate)); err != nil {
					return nil, err
				}
			}
		}
		// A required param must resolve to a non-empty value, including when it
		// was supplied explicitly but empty (e.g. --param REQUIRED=).
		if p.Required && strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("missing required parameter %q (provide with --param %s=<value>)", p.Name, p.Name)
		}
		if err := validateParamValue(p, value); err != nil {
			return nil, err
		}
		if value == "" && !p.Required {
			continue
		}
		out = append(out, agenticv1alpha1.ParamValue{Name: p.Name, Value: value})
	}
	return out, nil
}

// validateParamValue checks a value against the parameter's declared type.
func validateParamValue(p agenticv1alpha1.Param, value string) error {
	if value == "" {
		return nil
	}
	switch p.Type {
	case paramTypeNumber:
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Errorf("parameter %q must be a number, got %q", p.Name, value)
		}
	case paramTypeBoolean:
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("parameter %q must be a boolean, got %q", p.Name, value)
		}
	}
	return nil
}

func parseParamFlags(flags []string) (map[string]string, error) {
	out := make(map[string]string, len(flags))
	for _, f := range flags {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --param %q, expected key=value", f)
		}
		out[strings.TrimSpace(k)] = v
	}
	return out, nil
}

func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}

// previewAndCreate prints the YAML of obj, asks for confirmation (unless
// dryRun), and creates it.
func previewAndCreate(
	ctx context.Context, cl client.Client, obj client.Object, name, kind, namespace string, dryRun bool,
) error {
	data, err := yaml.Marshal(obj)
	if err != nil {
		return err
	}
	if dryRun {
		_, _ = fmt.Fprintln(os.Stdout, string(data))
		return nil
	}
	_, _ = fmt.Fprintln(os.Stdout, "\n"+string(data))
	proceed, err := confirm(fmt.Sprintf("Create %s %q in namespace %q?", kind, name, namespace), true)
	if err != nil {
		return err
	}
	if !proceed {
		_, _ = fmt.Fprintln(os.Stdout, "aborted")
		return nil
	}
	if err := cl.Create(ctx, obj); err != nil {
		return fmt.Errorf("failed to create %s: %w", kind, err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "%s %q created\n", kind, name)
	return nil
}
