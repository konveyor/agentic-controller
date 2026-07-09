# Migration Harness Configuration Guide

## Philosophy: Separate Configs for Different Use Cases

Migration-harness has its **own config** separate from goose's general config. This is intentional:

### Why Separate Configs?

**Goose Config (`~/.config/goose/config.yaml`)**
- Used for general goose work (coding, debugging, exploration)
- You might use a fast, cheap model like `gemini-1.5-flash` or `gpt-4o-mini`
- Optimized for many short interactions

**Migration-Harness Config (`~/.migration-harness/config`)**
- Used specifically for migrations (long-running, complex tasks)
- You likely want a powerful, accurate model like `gemini-2.5-pro`, `claude-opus-4-0`, or `gpt-4o`
- Optimized for quality over speed/cost

### Real-World Example

```
Daily work with goose:
  ~/.config/goose/config.yaml
    GOOSE_PROVIDER: openai
    GOOSE_MODEL: gpt-4o-mini          # Fast, cheap

Running migrations:
  ~/.migration-harness/config
    MH_PROVIDER: gcp_vertex_ai
    MH_MODEL: gemini-2.5-pro          # Powerful, accurate
```

**Result:** You save money on daily work while ensuring migrations are high quality.

---

## Creating Your Config

### Method 1: Interactive (Recommended for Local Installation)

```bash
migration-harness init
```

**What it does:**
1. Detects your goose config (shows you what you're using for general work)
2. Asks if you want to use the **same** model or a **different** one
3. Prompts for MH_PROVIDER and MH_MODEL
4. Saves to `~/.migration-harness/config`

### Method 2: Manual (Recommended for Docker)

```bash
mkdir -p ~/.migration-harness
cat > ~/.migration-harness/config <<'EOF'
MH_MODEL="gemini-2.5-pro"
MH_PROVIDER="gcp_vertex_ai"
MH_MAX_TURNS="200"
MH_MAX_FIX_ITERATIONS="3"
EOF
```

---

## Config Fields Explained

### MH_PROVIDER

The goose provider name. Must match a provider configured in `~/.config/goose/config.yaml`.

**Common values:**
- `gcp_vertex_ai` - Google Cloud Vertex AI (Gemini models)
- `openai` - OpenAI API (GPT models, or custom endpoints via base_url)
- `anthropic` - Anthropic API (Claude models)
- `ollama` - Local Ollama instance
- Custom provider name (if you configured one in goose)

**Example goose config for reference:**
```yaml
# ~/.config/goose/config.yaml
gcp_vertex_ai:
  project_id: "my-project"
  location: "us-east5"

GOOSE_PROVIDER: gcp_vertex_ai
GOOSE_MODEL: gemini-1.5-flash
```

**Corresponding migration-harness config:**
```bash
# ~/.migration-harness/config
MH_PROVIDER="gcp_vertex_ai"     # ← Must match a provider in goose config
MH_MODEL="gemini-2.5-pro"       # ← Can be DIFFERENT from GOOSE_MODEL
```

---

### MH_MODEL

The specific model name to use for migrations.

**Recommendations by provider:**

| Provider | Best for Migrations | Good for Testing | Fast/Cheap |
|---|---|---|---|
| `gcp_vertex_ai` | `gemini-2.5-pro` | `gemini-2.0-flash` | `gemini-1.5-flash` |
| `openai` | `o1`, `gpt-4o` | `gpt-4o-mini` | `gpt-4o-mini` |
| `anthropic` | `claude-opus-4-0` | `claude-sonnet-4-0` | `claude-haiku-3-5` |

**Why it matters:**
- **Plan quality:** More powerful models generate better migration plans (fewer missed dependencies)
- **Execution accuracy:** Better models make fewer mistakes during code transformation
- **Fix effectiveness:** Better models debug and fix issues faster

**Cost vs. Quality:**
- A migration might take 15-30 minutes with a powerful model
- Using a weak model might save $2 in API costs but produce a broken migration
- Re-running with a better model costs more time and money than using it first

---

### MH_MAX_TURNS

Maximum number of LLM turns per step (detect, plan, execute, verify).

**Default:** `200`

**When to adjust:**
- **Increase to 300+** for very large codebases (1000+ files)
- **Decrease to 100** for small projects (< 50 files) to save cost
- **Increase to 500** if using a weak model that needs more iterations

**What it controls:**
- Each "turn" = one LLM request/response cycle
- More turns = model can do more work (read more files, make more edits)
- Hitting the limit = step fails with "max turns reached"

**How it's used per step:**
- **Detect:** Usually < 5 turns (just builds graph)
- **Plan:** 12-50 turns (dynamically calculated based on project size)
- **Execute:** 50-150 turns (one turn per file + fixes)
- **Verify:** 20-50 turns (compile, fix, retry)

---

### MH_MAX_FIX_ITERATIONS

Maximum auto-fix attempts during the verify step.

**Default:** `3`

**When to adjust:**
- **Increase to 5** if using a weak model that needs more chances
- **Decrease to 1** if using a powerful model and want to fail fast
- **Increase to 10** for experimental runs where you want to see how far it can go

**What it controls:**
- After execution, verify runs `mvn clean compile` (or equivalent)
- If compile fails, verify analyzes errors and attempts fixes
- Each fix = one iteration
- Hitting the limit = migration succeeds but with compile errors

**Example flow:**
```
Iteration 1: Compile → 5 errors → Fix imports → Retry
Iteration 2: Compile → 2 errors → Fix annotations → Retry
Iteration 3: Compile → SUCCESS
```

---

## Common Configurations

### High Quality (Production Migrations)

```bash
MH_MODEL="gemini-2.5-pro"
MH_PROVIDER="gcp_vertex_ai"
MH_MAX_TURNS="200"
MH_MAX_FIX_ITERATIONS="3"
```

**Use when:** You need production-ready migrations with minimal manual fixes.

---

### Fast Iteration (Development/Testing)

```bash
MH_MODEL="gemini-2.0-flash"
MH_PROVIDER="gcp_vertex_ai"
MH_MAX_TURNS="100"
MH_MAX_FIX_ITERATIONS="2"
```

**Use when:** Testing migration-harness itself, experimenting with prompts, or migrating very small projects.

---

### Maximum Quality (Critical Migrations)

```bash
MH_MODEL="claude-opus-4-0"
MH_PROVIDER="anthropic"
MH_MAX_TURNS="300"
MH_MAX_FIX_ITERATIONS="5"
```

**Use when:** Migrating mission-critical applications where quality matters more than cost/time.

---

### Custom LiteLLM Endpoint

```bash
MH_MODEL="Qwen3.6-35B-A3B"
MH_PROVIDER="openai"
MH_MAX_TURNS="200"
MH_MAX_FIX_ITERATIONS="3"
```

**Plus in goose config:**
```yaml
# ~/.config/goose/config.yaml
openai:
  api_key: "your-litellm-key"
  base_url: "https://your-litellm-endpoint.com/v1"
```

**Use when:** Using a custom model via LiteLLM proxy.

---

## Verifying Your Config

### Check Migration-Harness Config

```bash
cat ~/.migration-harness/config
```

Expected:
```
MH_MODEL="gemini-2.5-pro"
MH_PROVIDER="gcp_vertex_ai"
MH_MAX_TURNS="200"
MH_MAX_FIX_ITERATIONS="3"
```

### Check Goose Config

```bash
cat ~/.config/goose/config.yaml
```

Look for:
```yaml
gcp_vertex_ai:          # ← This provider must exist
  project_id: "..."
  location: "..."

GOOSE_PROVIDER: gcp_vertex_ai
GOOSE_MODEL: gemini-1.5-flash
```

### Test That Provider Works

```bash
goose run -t "Hello"
```

Expected: Response from the LLM (proves goose can reach the provider)

---

## Troubleshooting

### Error: "Config not found"

**Symptom:**
```
✗ Config not found: /root/.migration-harness/config
```

**Solution:**
```bash
# Create config
migration-harness init
# OR manually:
cat > ~/.migration-harness/config <<'EOF'
MH_MODEL="gemini-2.5-pro"
MH_PROVIDER="gcp_vertex_ai"
MH_MAX_TURNS="200"
MH_MAX_FIX_ITERATIONS="3"
EOF
```

---

### Error: "Unknown provider"

**Symptom:**
```
Error: Unknown provider: gcp_vertex_ai
```

**Cause:** MH_PROVIDER doesn't match any provider in goose config.

**Solution:**
1. Check what providers goose knows about:
   ```bash
   cat ~/.config/goose/config.yaml | grep -E "^[a-z_]+:"
   ```
2. Update MH_PROVIDER to match:
   ```bash
   nano ~/.migration-harness/config
   # Change MH_PROVIDER to match a goose provider
   ```

---

### Error: "Model not found"

**Symptom:**
```
Error: The model `gemini-2.5-pro` does not exist
```

**Cause:** MH_MODEL doesn't exist for the given provider.

**Solution:**
1. List available models for your provider (provider-specific)
2. Update MH_MODEL:
   ```bash
   nano ~/.migration-harness/config
   # Change MH_MODEL to a valid model name
   ```

---

## Summary

✅ **Migration-harness config is separate from goose config**  
✅ **Choose a powerful model for migrations** (different from daily work)  
✅ **Create config via `migration-harness init` or manually**  
✅ **Config lives at `~/.migration-harness/config`**  
✅ **Provider must exist in `~/.config/goose/config.yaml`**  

**Key Insight:** Don't use the same model for everything. Migrations are complex tasks that benefit from powerful models.
