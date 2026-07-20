package goose

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/konveyor/migration-harness/internal/acp"
	"gopkg.in/yaml.v3"
)

// Recipe represents a goose recipe YAML file.
type Recipe struct {
	Version     string        `yaml:"version"`
	Title       string        `yaml:"title"`
	Description string        `yaml:"description"`
	Settings    RecipeSettings `yaml:"settings"`
	Parameters  []RecipeParam `yaml:"parameters"`
	Extensions  []RecipeExt   `yaml:"extensions"`
	Instruction string        `yaml:"instructions"`
	Prompt      string        `yaml:"prompt"`
	Response    RecipeResp    `yaml:"response"`
}

type RecipeSettings struct {
	Temperature float64 `yaml:"temperature"`
}

type RecipeParam struct {
	Key         string `yaml:"key"`
	InputType   string `yaml:"input_type"`
	Requirement string `yaml:"requirement"`
	Description string `yaml:"description"`
}

type RecipeExt struct {
	Type    string `yaml:"type"`
	Name    string `yaml:"name"`
	Timeout int    `yaml:"timeout"`
	Bundled bool   `yaml:"bundled"`
}

type RecipeResp struct {
	JSONSchema yaml.Node `yaml:"json_schema"`
}

// SchemaJSON converts the YAML json_schema node to JSON bytes.
func (r *RecipeResp) SchemaJSON() ([]byte, error) {
	if r.JSONSchema.Kind == 0 {
		return nil, nil
	}
	var raw any
	if err := r.JSONSchema.Decode(&raw); err != nil {
		return nil, err
	}
	return json.Marshal(raw)
}

// ParseRecipe reads and parses a goose recipe YAML file.
func ParseRecipe(path string) (*Recipe, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read recipe %s: %w", path, err)
	}

	var r Recipe
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse recipe %s: %w", path, err)
	}

	return &r, nil
}

var templatePattern = regexp.MustCompile(`\{\{\s*(\w+)\s*\}\}`)

// Render substitutes {{ key }} placeholders in instructions and prompt
// with values from params. Returns an error if any required parameter
// is missing.
func (r *Recipe) Render(params map[string]string) (instructions string, prompt string, err error) {
	instructions = renderTemplate(r.Instruction, params)
	prompt = renderTemplate(r.Prompt, params)

	// Check for unsubstituted placeholders
	remaining := templatePattern.FindAllString(instructions+prompt, -1)
	if len(remaining) > 0 {
		return "", "", fmt.Errorf("unsubstituted template parameters: %v", remaining)
	}

	return instructions, prompt, nil
}

func renderTemplate(tmpl string, params map[string]string) string {
	return templatePattern.ReplaceAllStringFunc(tmpl, func(match string) string {
		key := strings.TrimSpace(match[2 : len(match)-2])
		if val, ok := params[key]; ok {
			return val
		}
		return match
	})
}

// MCPServers translates recipe extensions to ACP MCPServer configs.
// For builtin extensions (like "developer"), goose serve already has
// them via --with-builtin. Returns an empty list for builtins.
func (r *Recipe) MCPServers() []acp.MCPServer {
	var servers []acp.MCPServer
	for _, ext := range r.Extensions {
		if ext.Bundled {
			continue
		}
		servers = append(servers, acp.MCPServer{
			Command: ext.Name,
			Args:    []string{},
		})
	}
	return servers
}

// BuildACPPrompt combines rendered instructions, prompt, and response
// schema into a single prompt string for ACP session/prompt.
func (r *Recipe) BuildACPPrompt(instructions, prompt string) (string, error) {
	var b strings.Builder
	b.WriteString(instructions)
	b.WriteString("\n\n")
	b.WriteString(prompt)

	schemaBytes, err := r.Response.SchemaJSON()
	if err != nil {
		return "", fmt.Errorf("marshal response schema: %w", err)
	}
	if len(schemaBytes) > 0 {
		b.WriteString("\n\nYou MUST return your response as a JSON object matching this schema:\n")
		b.Write(schemaBytes)
	}

	return b.String(), nil
}
