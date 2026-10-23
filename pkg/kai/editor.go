package kai

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// editResource fetches obj, opens its YAML representation in the user's $EDITOR
// (kubectl-edit style), and applies the edited result. obj must be a pointer to
// a typed resource; it is populated in place. A no-op edit is reported and no
// update is issued.
func editResource(ctx context.Context, cl client.Client, obj client.Object) error {
	key := client.ObjectKeyFromObject(obj)
	if err := cl.Get(ctx, key, obj); err != nil {
		return fmt.Errorf("failed to get %s/%s: %w", key.Namespace, key.Name, err)
	}

	// Strip server-managed noise so the editable document is clean.
	obj.SetManagedFields(nil)

	original, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal resource to YAML: %w", err)
	}

	edited, err := openInEditor(original)
	if err != nil {
		return err
	}

	if bytes.Equal(bytes.TrimSpace(original), bytes.TrimSpace(edited)) {
		_, _ = fmt.Fprintf(os.Stdout, "no changes made to %s\n", key.Name)
		return nil
	}

	updated, err := applyEdit(obj, edited)
	if err != nil {
		return err
	}

	if err := cl.Update(ctx, updated); err != nil {
		return fmt.Errorf("failed to apply changes: %w", err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "%s updated\n", key.Name)
	return nil
}

// applyEdit decodes the edited YAML into a fresh zero-valued object of the same
// concrete type as current. Decoding into a new object (rather than mutating the
// fetched one) is what makes field deletions take effect: sigs.k8s.io/yaml skips
// fields absent from the document, so a field cleared in the editor would
// otherwise keep its fetched value. The object's identity and resourceVersion are
// restored from current so the update targets the right object under optimistic
// concurrency even if the user edited those out.
func applyEdit(current client.Object, edited []byte) (client.Object, error) {
	fresh, ok := reflect.New(reflect.TypeOf(current).Elem()).Interface().(client.Object)
	if !ok {
		return nil, fmt.Errorf("cannot build an empty %T to decode into", current)
	}
	if err := yaml.Unmarshal(edited, fresh); err != nil {
		return nil, fmt.Errorf("edited YAML is invalid: %w", err)
	}
	fresh.SetName(current.GetName())
	fresh.SetNamespace(current.GetNamespace())
	fresh.SetResourceVersion(current.GetResourceVersion())
	return fresh, nil
}

// openInEditor writes content to a temporary file, opens it in $EDITOR (falling
// back to vi), and returns the edited content.
func openInEditor(content []byte) ([]byte, error) {
	f, err := os.CreateTemp("", "kubectl-kai-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()

	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, err
	}

	editor := editorCommand()
	parts := strings.Fields(editor)
	parts = append(parts, name)
	cmd := exec.Command(parts[0], parts[1:]...) //nolint:gosec // user's own $EDITOR
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("editor exited with error: %w", err)
	}

	return os.ReadFile(name)
}

// editorCommand resolves the editor to use, honoring $KUBE_EDITOR then $EDITOR
// then falling back to vi.
func editorCommand() string {
	for _, env := range []string{"KUBE_EDITOR", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v
		}
	}
	return "vi"
}
