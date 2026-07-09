# Migration Harness - Presentation Deck V2
## Human-Guided, LLM-Amplified Code Migration

**Target Audience:** Engineering Team  
**Duration:** 15-20 minutes  
**Format:** 6 slides + optional live demo

**Key Framing:** This is NOT "AI replaces engineers" — it's "Engineers teach AI, AI amplifies engineers"

---

# Slide 1: The Problem - Why Enterprise Migrations Are Hard

## The Reality of Manual Migrations

```
Java EE → Quarkus migration for a 50K LOC enterprise app:
├─ 3-6 months senior engineer time
├─ Dependency hell: fix service layer → breaks API layer
├─ Tribal knowledge bottleneck: "Only Sarah knows how our custom auth works"
├─ Quality varies: different engineers make different choices
└─ No learning: same mistakes every migration
```

## The Real Pain Points

### 1. Order Matters, But It's Not Obvious
```
Wrong order:
  Fix API endpoints first → compilation errors in services
  Fix services → now API errors make sense, wasted work

Right order:
  Build config → Models → Services → API
  By the time you reach API, most errors already resolved
```

**Why this is hard:**
- Dependency graphs are complex (800 classes, who depends on what?)
- Enterprise apps have custom layers (your framework, not textbook MVC)
- One wrong decision cascades errors

---

### 2. Enterprise Complexity LLMs Don't Know

Your codebase has:
- **Custom frameworks:** `@OurCompanyAuth`, `@LegacyCache` — not in training data
- **Internal patterns:** "All services must extend `BaseEnterpriseService`"
- **Org-specific rules:** "Never touch `PaymentProcessor` without compliance review"
- **Historical quirks:** "That timeout is 5000ms because of the 2019 incident"

**Traditional automation:** Breaks on first custom annotation  
**LLM without guidance:** Guesses wrong, creates more problems  
**What we need:** Human-in-the-loop to inject tribal knowledge

---

### 3. "Fixed" ≠ Perfect, But Progress Is Victory

**Unrealistic goal:**
```
Input: 50K LOC Java EE app with 247 compilation errors
Output: ✅ BUILD SUCCESS, 0 errors, production ready
```

**Realistic goal:**
```
Input: 50K LOC Java EE app with 247 compilation errors
Output: ⚠️ 23 errors remaining (90% reduction)
        ✅ All mechanical transformations done
        ✅ Clear list of what needs human attention
        ✅ Lessons learned: where to improve next time
```

**Why this is actually valuable:**
- Eliminated 224 tedious errors (javax→jakarta, @Stateless→@ApplicationScoped)
- Remaining 23 errors are **meaningful** (require architectural decisions)
- Engineer focuses on high-value work, not mechanical drudgery

---

### 4. No Learning Loop = Repeating Mistakes

**Current state:**
```
Migration #1: Engineer fixes 247 errors manually, takes 2 weeks
Migration #2: Different engineer, similar app, same 247 errors, 2 weeks again
Migration #3: ... still learning the same lessons
```

**What's missing:**
- ❌ No capture of "what worked"
- ❌ No improvement of references/rulesets
- ❌ No feedback loop

---

**What if we could:**
1. ✅ **Automate dependency-aware ordering** (graph-powered)
2. ✅ **Inject tribal knowledge at planning phase** (human approval gate)
3. ✅ **Reduce errors significantly** (not eliminate, but 80-90% reduction)
4. ✅ **Learn from every migration** (lessons → better references)

---

# Slide 2: The Solution - Architecture for Human-LLM Collaboration

## Core Philosophy

> **Humans decide WHAT to do (architecture, order, tribal knowledge)**  
> **LLMs execute HOW to do it (mechanical transformations at scale)**  
> **Both teach each other (humans improve references, LLMs surface lessons)**

---

## 5-Step Pipeline

```
┌─────────────┐
│   DETECT    │  Build dependency graph: 4,275 nodes, 8,386 edges, 615 communities
│   (6s)      │  Identify layers: build(15 files) → models(47) → services(23) → API(12)
└──────┬──────┘  Goal: Know the terrain before planning
       │
┌──────▼──────┐
│    PLAN     │  LLM reads reference + graph → generates 35-step plan
│   (4.6m)    │  Proposes order: Layer 1 (build) → 2 (models) → 3 (services) → 4 (API)
└──────┬──────┘  Output: PLAN.md with ⚠️ marks for complex/custom patterns
       │
       ├──────► 👤 HUMAN APPROVAL GATE
       │        ├─ Review: Is order correct for OUR architecture?
       │        ├─ Inject: Add org-specific knowledge ("skip PaymentService, legacy")
       │        ├─ Adjust: Reorder if graph missed custom dependencies
       │        └─ Decision: y=proceed / edit=modify plan / N=abort
       │
┌──────▼──────┐
│  EXECUTE    │  LLM executes each step: read → transform → write
│  (12.6m)    │  Logs lessons per step: "Step 12 had @CustomAuth, pattern not in reference"
└──────┬──────┘  Result: 32/35 steps succeeded, 3 flagged for manual review
       │
┌──────▼──────┐
│   VERIFY    │  mvn compile → 247 errors → analyze → fix → 89 errors → fix → 23 errors
│   (56s)     │  Goal: NOT zero errors — reduce to manageable, meaningful errors
└──────┬──────┘  Output: What's left and WHY (missing org patterns, custom frameworks)
       │
┌──────▼──────┐
│  LESSONS    │  Aggregate: Where did LLM hit limits? What patterns were missed?
│             │  Update references: Add @CustomAuth pattern → fewer errors next time
└─────────────┘  Learning loop: Each migration improves the next
```

---

## Key Design Principles

### 1. **Dependency-Aware Execution Order**

**Why order matters:**
```
Wrong: Fix API first
  OrderEndpoint.java: Cannot find symbol OrderService (breaks)
  ↓ LLM tries to fix by importing different OrderService (wrong fix)
  ↓ Now ServiceLayer has cascading errors
  ✗ Result: 247 errors → 310 errors (made it worse!)

Right: Fix dependencies first (graph-driven)
  1. Build config (pom.xml) — no dependencies
  2. Models (Order.java, Product.java) — depend only on build
  3. Services (OrderService.java) — depend on models
  4. API (OrderEndpoint.java) — depend on services
  ✓ Result: 247 errors → 23 errors (layer-by-layer resolution)
```

**How we achieve this:**
- Graph detects communities (architectural layers)
- Plan orders steps by layer (build → models → services → API)
- Execute follows the order (no jumping layers)

---

### 2. **Interactive Planning for Tribal Knowledge**

**What graph can't detect:**
```yaml
# Graph sees:
OrderService.java → PaymentProcessor.java (dependency edge)

# Graph doesn't know:
PaymentProcessor.java:
  - Custom annotation: @ComplianceRequired
  - Org rule: "Must get security review before touching"
  - Historical context: "Contains PCI-DSS logic, changed in 2019 audit"
  - Migration strategy: "Keep as-is, will migrate separately in Q3"
```

**Human adds this at approval gate:**
```markdown
# PLAN.md (LLM-generated)
### Step 18: Migrate PaymentProcessor.java
- File: src/main/java/com/company/payment/PaymentProcessor.java
- Action: MODIFY
- Transform: @Stateless → @ApplicationScoped

# PLAN.md (after human edit)
### Step 18: ⚠️ SKIP PaymentProcessor.java — HUMAN REVIEW REQUIRED
- File: src/main/java/com/company/payment/PaymentProcessor.java
- Action: SKIP
- Reason: PCI-DSS compliance, requires security review
- Owner: @security-team
- Migration: Deferred to Q3 dedicated sprint
```

**Result:** LLM doesn't break critical code it doesn't understand

---

### 3. **Success = Significant Error Reduction, Not Perfection**

**Reframe expectations:**

| Metric | Naive Goal | Realistic Goal | Our Result |
|---|---|---|---|
| Compilation errors | 0 (100% fixed) | 10-20% remaining | 23/247 (9.3% remaining) |
| Manual work | None | Focused, high-value | 23 errors, all require architectural decisions |
| Time saved | 100% | 80-90% | 91% of mechanical work automated |
| Production ready | Immediate | 1-2 days review | Ready after targeted fixes |

**Why 9% errors remaining is a WIN:**
```
247 errors before:
  ├─ 180 errors: javax.* → jakarta.* (mechanical, tedious)
  ├─  44 errors: @Stateless → @ApplicationScoped (pattern-based)
  └─  23 errors: Custom patterns (@CompanyAuth, internal framework)

After migration-harness:
  ├─ 180 javax errors: ✅ FIXED (automated)
  ├─  44 annotation errors: ✅ FIXED (reference-driven)
  └─  23 custom errors: ⚠️ FLAGGED (need human expertise)

Engineer work:
  Before: 2 weeks fixing all 247 (most are boring)
  After: 2 days fixing 23 meaningful errors
  Time saved: 87.5%
```

**The 23 remaining errors are GOOD errors:**
- They surface tribal knowledge gaps
- They require architectural decisions
- They teach us what to add to references
- They're where engineers add real value

---

### 4. **Lessons Feed Future Migrations**

**Learning loop:**
```
Migration #1 (CoolStore):
  Execute step 12: OrderService.java
  ├─ LLM sees: @CompanyAuth (custom annotation, not in reference)
  ├─ LLM tries: Remove it (guesses it's legacy)
  ✗ Compilation error: Authentication fails
  
  Lesson logged:
    "Step 12: @CompanyAuth is required by internal framework.
     Reference javaee-quarkus.md doesn't cover custom auth patterns.
     Manual fix: Keep @CompanyAuth, add @ApplicationScoped alongside it."

Update reference:
  # javaee-quarkus.md (after migration #1)
  
  ## Pattern: Custom Framework Annotations
  
  // BEFORE
  @Stateless
  @CompanyAuth(roles = "admin")
  public class OrderService { }
  
  // AFTER
  @ApplicationScoped
  @CompanyAuth(roles = "admin")  ← KEEP custom framework annotations
  public class OrderService { }

Migration #2 (InventoryApp - similar app):
  Execute step 8: InventoryService.java
  ├─ LLM sees: @CompanyAuth
  ├─ LLM reads reference: Pattern says keep it
  ✓ Keeps @CompanyAuth, adds @ApplicationScoped
  ✓ No compilation error
  
  Result: Same pattern, no human intervention needed
```

**This is the real value:**
- First migration: Learn hard lessons
- Update references: Capture tribal knowledge
- Future migrations: Avoid same mistakes
- Organization knowledge: Codified, not locked in heads

---

# Slide 3: Key Innovation #1 - Graph-Powered Dependency Ordering

## The "Last Node First" Problem

**Scenario: Fix API layer first (wrong order)**

```
Before migration:
  pom.xml (Java EE dependencies) ──┐
    ↓                               ↓
  OrderService.java (@Stateless)   ProductService.java (@Stateless)
    ↓                               ↓
  OrderEndpoint.java (@Path)       ProductEndpoint.java (@Path)

Step 1: Migrate OrderEndpoint.java (API layer)
  - Change @Path imports
  - Update injection (@EJB → @Inject)
  - Compile check...
  
  ✗ Error: Cannot find symbol OrderService.class
     (because OrderService is still @Stateless EJB, but pom.xml still Java EE)
  
  LLM sees error, tries to fix:
    - Adds back @EJB import (wrong direction!)
    - Or tries to change OrderService (out of scope for this step)
    - Creates confusion, extra errors

Result: 247 errors → 310 errors (cascading failures)
```

---

**Correct approach: Build dependency graph, fix bottom-up**

```
Graph analysis (via graphify):
  Community detection reveals 4 layers:
  
  Layer 1 (build):
    pom.xml (0 dependencies on code)
  
  Layer 2 (models):
    Order.java, Product.java (depend only on JPA → jakarta.persistence)
  
  Layer 3 (services):
    OrderService.java → Order.java (depends on Layer 2)
    ProductService.java → Product.java (depends on Layer 2)
  
  Layer 4 (API):
    OrderEndpoint.java → OrderService.java (depends on Layer 3)
    ProductEndpoint.java → ProductService.java (depends on Layer 3)

Migration order (bottom-up):
  Step 1-5: Layer 1 (pom.xml, application.properties)
    ✓ Result: Quarkus dependencies now available
  
  Step 6-15: Layer 2 (models)
    ✓ Result: @Entity classes use jakarta.persistence
  
  Step 16-25: Layer 3 (services)
    ✓ Result: Services are @ApplicationScoped, inject models correctly
    ✓ Crucially: When we fix services, models are already fixed
  
  Step 26-35: Layer 4 (API)
    ✓ Result: Endpoints inject services correctly
    ✓ By now: Most compilation errors already resolved by lower layers

Final: 247 errors → 23 errors (90.7% reduction)
```

**Why this works:**
- Dependencies flow upward (API → Services → Models → Build)
- Fix from bottom (Build) upward (API)
- Each layer builds on stable foundation
- Errors cascade down, not up
- **By the time you fix "last node" (API), earlier nodes already clean**

---

## How Graph Detects This

**Graphify output (from detect step):**

```json
{
  "communities": [
    {
      "id": 0,
      "label": "build-config",
      "size": 15,
      "files": ["pom.xml", "application.properties", ...],
      "inbound_edges": 0,
      "outbound_edges": 247
    },
    {
      "id": 47,
      "label": "domain-models",
      "size": 23,
      "files": ["Order.java", "Product.java", ...],
      "inbound_edges": 3,
      "outbound_edges": 89
    },
    {
      "id": 12,
      "label": "business-services",
      "size": 18,
      "files": ["OrderService.java", ...],
      "inbound_edges": 47,
      "outbound_edges": 15
    },
    {
      "id": 3,
      "label": "rest-api",
      "size": 8,
      "files": ["OrderEndpoint.java", ...],
      "inbound_edges": 73,
      "outbound_edges": 0
    }
  ]
}
```

**LLM reads this and generates plan:**
```markdown
# PLAN.md

## Migration Order (by architectural layer)

Phase 1: Build config (community #0)
  - Steps 1-5: pom.xml, application.properties
  - Rationale: 0 inbound edges = no dependencies, safe to migrate first

Phase 2: Domain models (community #47)
  - Steps 6-15: Order.java, Product.java, etc.
  - Rationale: Low inbound edges, depend only on build config

Phase 3: Business services (community #12)
  - Steps 16-25: OrderService.java, ProductService.java
  - Rationale: Depend on models (already migrated)

Phase 4: REST API (community #3)
  - Steps 26-35: OrderEndpoint.java, ProductEndpoint.java
  - Rationale: 0 outbound edges = top of dependency tree, migrate last
```

**Key insight:**
> Community detection + edge analysis = Automatically discover migration order

---

## The "God Nodes" Problem

**Some files are highly connected:**

```
ShoppingCartService.java:
  - degree: 47 (connects to 47 other nodes)
  - Called by: 12 endpoints
  - Calls: 8 services, 15 models
  - Imports: 23 different packages
  
Graph flags this as "god node" → LLM marks it:
  ⚠️ Step 22: ShoppingCartService.java — HIGH RISK
     Reason: 47 connections, changes ripple across system
     Recommendation: Test thoroughly after this step
```

**Human sees this in plan:**
- "Oh right, ShoppingCartService is our core logic"
- "Let's be extra careful here, maybe manual review"

---

# Slide 4: Key Innovation #2 - Reference-Driven + Lesson-Driven Improvement

## The Learning Loop

```
┌──────────────────────────────────────────────────────────┐
│  Migration #1: Discover tribal knowledge gaps            │
│  ├─ LLM encounters @CompanyAuth (unknown pattern)        │
│  ├─ Tries to remove it (guess based on @Stateless)       │
│  ├─ Compilation error                                    │
│  └─ Lesson logged: "@CompanyAuth required by framework"  │
└─────────────┬────────────────────────────────────────────┘
              │
              ▼
┌──────────────────────────────────────────────────────────┐
│  Engineer reviews lessons:                               │
│  ├─ "Ah, all our services use @CompanyAuth"              │
│  ├─ "LLM doesn't know this, we need to teach it"         │
│  └─ Decision: Update javaee-quarkus.md reference         │
└─────────────┬────────────────────────────────────────────┘
              │
              ▼
┌──────────────────────────────────────────────────────────┐
│  Update reference (capture tribal knowledge):            │
│                                                           │
│  ## Pattern: Company Framework Annotations               │
│                                                           │
│  // BEFORE                                               │
│  @Stateless                                              │
│  @CompanyAuth(roles = {"admin", "user"})                 │
│  public class OrderService { }                           │
│                                                           │
│  // AFTER                                                │
│  @ApplicationScoped                                      │
│  @CompanyAuth(roles = {"admin", "user"})  ← PRESERVE    │
│  public class OrderService { }                           │
│                                                           │
│  **Rule:** Keep all @CompanyAuth, @CompanyCache,         │
│  @CompanyAudit annotations — internal framework          │
└─────────────┬────────────────────────────────────────────┘
              │
              ▼
┌──────────────────────────────────────────────────────────┐
│  Migration #2: Reference prevents same error             │
│  ├─ LLM reads updated reference                          │
│  ├─ Sees pattern: keep @CompanyAuth                      │
│  ├─ Applies correctly to InventoryService.java           │
│  └─ ✓ No error this time                                 │
└──────────────────────────────────────────────────────────┘
```

---

## Lesson Format (execution-log.md)

**Not just logging, teaching:**

```markdown
# Execution Log

## Step 12: Migrate OrderService.java ✓

**Status:** ok  
**Duration:** 18s  
**Files changed:** 1

### What was done:
- Removed `@Stateless` annotation
- Added `@ApplicationScoped` from jakarta.enterprise.context
- Changed `@EJB` injections to `@Inject`
- Updated imports: javax.ejb.* → jakarta.inject.*

### Lesson learned:
⚠️ **Pattern gap identified:** @CompanyAuth annotation

This file uses `@CompanyAuth(roles = "admin")` — a custom annotation 
from the internal company framework. The reference file 
`javaee-quarkus.md` doesn't document company-specific patterns.

**What I did:** Preserved @CompanyAuth (assumed it should remain)  
**Result:** Compilation succeeded  
**Recommendation:** Add company framework patterns to reference

**For future migrations:**
1. Document pattern in javaee-quarkus.md under "Company Patterns"
2. Clarify: Which custom annotations should be preserved?
3. Example: @CompanyAuth, @CompanyCache, @CompanyAudit, etc.

---

## Step 24: Migrate OrderServiceMDB.java ⚠️

**Status:** partial  
**Duration:** 34s  
**Files changed:** 1

### What was done:
- Converted @MessageDriven → @ApplicationScoped
- Replaced onMessage(Message) → onMessage(String) with @Incoming
- Added @Blocking annotation
- Created application.properties channel config

### Lesson learned:
⚠️ **Complexity hit:** Channel naming mismatch

Plan specified channel name: `orders-incoming`
Application.properties configured: `orders-incoming`
But code also has `InventoryNotificationMDB` using same channel.

**Issue:** Two consumers on same channel without broadcast config
**Risk:** Only one consumer will receive messages (not both)
**Missing in reference:** Broadcast configuration pattern

**What should happen:**
```properties
mp.messaging.incoming.orders-incoming.broadcast=true
```

**For future migrations:**
1. Update reference: Add broadcast pattern for multi-consumer channels
2. Add verification step: Check for duplicate @Incoming channel names
3. Recommend: Use separate channels unless broadcast intended

---

## Step 35: Delete WebLogic stubs ✓

**Status:** ok  
**Duration:** 3s  
**Files deleted:** 8

### Lesson learned:
⚠️ **Dead code detection needed**

Deleted `src/main/java/weblogic/` directory (8 stub files).
However, found one reference remaining:

`ShoppingCartService.java:127` still has:
```java
import weblogic.jms.extensions.WLMessageProducer;
```

**This import is now broken** (weblogic/ deleted).

**What I did:** Left it for verification phase to catch  
**Result:** Verification caught this, auto-fix removed dead import  
**Issue:** Inefficient — should have caught during execution

**For future migrations:**
1. Before deleting directories, grep for remaining references
2. Add step: "Scan for imports matching deleted package"
3. Fix references BEFORE deletion, not after
```

---

## What These Lessons Enable

### 1. **Engineer Knows Where to Focus**
Reading execution-log.md shows:
- ✅ Steps 1-11: Clean, no issues
- ⚠️ Step 12: Custom annotation (review if behavior correct)
- ✅ Steps 13-23: Clean
- ⚠️ Step 24: Broadcast config needed (add 1 line to properties)
- ✅ Steps 25-35: Clean

**Engineer action:** 2 targeted fixes, not 35 file reviews

---

### 2. **Reference Improvement is Systematic**
Extract all "⚠️ Pattern gap" lessons:
```bash
grep "Pattern gap" execution-log.md

Step 12: @CompanyAuth annotation
Step 24: Broadcast configuration  
Step 27: Custom datasource name pattern
```

**Update reference with these 3 patterns → next migration cleaner**

---

### 3. **Organizational Knowledge Captured**

Traditional migration:
```
Engineer 1 fixes @CompanyAuth issue → knowledge in their head
Engineer 2 (6 months later) hits same issue → learns again
```

Migration-harness:
```
Migration #1: Hits @CompanyAuth → lesson logged
Engineer updates reference → pattern documented
Migration #2: Reference has pattern → no error
```

**Tribal knowledge → Codified knowledge**

---

# Slide 5: What Makes This Production-Ready - Design Decisions

## 1. **Interactive Planning = Inject Tribal Knowledge**

**Not just "approve or reject" — Edit the plan:**

```markdown
# PLAN.md (LLM generated)

### Step 18: Migrate PaymentProcessor.java
- File: src/main/java/com/company/payment/PaymentProcessor.java
- Action: MODIFY
- Pattern: @Stateless → @ApplicationScoped
```

**Human edits (adds context LLM doesn't have):**

```markdown
### Step 18: ⚠️ SKIP PaymentProcessor.java — COMPLIANCE REQUIRED
- File: src/main/java/com/company/payment/PaymentProcessor.java
- Action: SKIP
- Reason: PCI-DSS compliance code, requires security audit before changes
- Owner: @security-team
- Migration: Deferred to Q3 security sprint
- Workaround: Leave as Java EE service, Quarkus can call it via HTTP
```

**LLM reads edited plan:**
- Sees "SKIP" action for Step 18
- Doesn't modify PaymentProcessor.java
- Logs: "Step 18 skipped per human override"

**Result:** Critical code protected by human judgment

---

**Another edit example: Reorder steps**

```markdown
# PLAN.md (LLM generated order)
Step 20: ShoppingCartService.java (high complexity, 47 connections)
Step 21: OrderService.java

# PLAN.md (human reordered)
Step 20: OrderService.java          ← MOVED UP
  Reason: OrderService is simpler, test first
Step 21: ShoppingCartService.java   ← MOVED DOWN
  Reason: High complexity, do after simpler services work
  Add: Run mvn compile after this step (checkpoint)
```

**Why this matters:**
- Human knows OrderService is "safer" despite graph saying otherwise
- Checkpoint after risky step catches errors early
- LLM executes in human-specified order

---

## 2. **Separate Model Selection by Task**

**Philosophy:** Migration is not coding, use different models

```
Daily coding (goose):                Migration (migration-harness):
├─ Tasks: "add a test", "fix bug"   ├─ Task: 35-step architectural transformation
├─ Duration: 30s - 5min             ├─ Duration: 15-30 minutes
├─ Model: gemini-1.5-flash          ├─ Model: gemini-2.5-pro or claude-opus-4-0
├─ Cost: $0.01 per task             ├─ Cost: $3-5 per migration
└─ Priority: Speed, iteration       └─ Priority: Correctness, completeness

Config separation:
  ~/.config/goose/config.yaml         ~/.migration-harness/config
    GOOSE_MODEL: gemini-1.5-flash       MH_MODEL: gemini-2.5-pro
```

**ROI:**
```
Cost difference: $2.80 cheaper model vs $3.50 powerful model = $0.70 saved
Risk: Cheaper model misses @CompanyAuth pattern → 2 hours debugging
Time value: $0.70 saved, $200 engineer time wasted

Conclusion: Always use powerful model for migrations
```

---

## 3. **Eval Framework = Scientific Model Comparison**

**Problem:** How do we know which model is best?

**Traditional approach:** "Try it and see" (anecdotal)

**Our approach:** LLM-as-judge with standardized criteria

```bash
# Run same migration with 3 models
for model in claude-opus-4-6 gemini-2.5-pro gpt-4o; do
  migration-harness ~/coolstore "Migrate Java EE to Quarkus" \
    --model $model --output ~/eval/$model/coolstore
done

# Each produces:
~/eval/claude-opus-4-6/coolstore/
  ├─ PLAN.md
  ├─ execution-log.md
  ├─ verification-report.md
  └─ metrics.json

# Compare using eval-harness (uses Claude Opus 4.6 as judge)
eval-harness --compare ~/eval/*/coolstore
```

**Evaluation dimensions:**
1. **Plan Quality (0-10):**
   - Completeness: Caught all needed transformations?
   - Detail: Steps specific and actionable?
   - Ordering: Respected dependencies?
   - Reference usage: Applied patterns correctly?

2. **Execution Quality (0-10):**
   - Lessons documented: Captured gaps, not just actions?
   - Errors logged: Transparent about failures?
   - Scope discipline: Stayed focused, no over-engineering?
   - Files touched: Only necessary modifications?

3. **Verification Success (0-10):**
   - Build status: How many errors reduced?
   - Auto-fix effectiveness: Fixes targeted and successful?

4. **Overall (weighted):**
   (plan × 0.3) + (execution × 0.2) + (verification × 0.5)
   Why this weighting? Verification matters most (did it work?)

---

**Example comparison output:**

```markdown
# Model Comparison: CoolStore Java EE → Quarkus

## Results Table

| Model | Plan | Exec | Verify | Overall | Duration | Errors Remaining |
|---|---|---|---|---|---|---|
| Claude Opus 4.6 | 9/10 | 9/10 | 7/10 | **8.0/10** | 18.2m | 23/247 (9.3%) |
| Gemini 2.5 Pro | 8/10 | 6/10 | 7/10 | **7.1/10** | 21.1m | 34/247 (13.8%) |
| GPT-4o | 7/10 | 7/10 | 6/10 | **6.7/10** | 19.5m | 41/247 (16.6%) |

## Skill Gap Analysis (Common Failures)

All 3 models struggled with:
1. **Reactive messaging broadcast config**
   - Evidence: Steps 24-25 in all execution logs
   - Missed: `mp.messaging.incoming.*.broadcast=true` when multiple consumers
   - Fix: Add broadcast pattern to javaee-quarkus.md

2. **Version-specific artifact names**
   - Evidence: Claude Opus used `quarkus-rest-jackson` (3.9+) for 3.8.4 target
   - Fix: Add version mapping table to reference

3. **Dead code removal after dependency deletion**
   - Evidence: All models deleted audit library but missed code references
   - Fix: Add step template: "grep for imports from deleted dependencies"

## Recommendation

**For production migrations:** Claude Opus 4.6 or Gemini 2.5 Pro
- Opus: Highest quality, best lesson capture
- Gemini: 90% quality, 15% faster, slightly cheaper

**For experimentation:** GPT-4o acceptable
- Lower quality but still 83% error reduction

**Update references based on common gaps → re-run → measure improvement**
```

**Why this matters:**
- ✅ Data-driven: Know which model for which migration type
- ✅ Actionable: Shows what to improve in references
- ✅ Measurable: Track quality over time

---

## 4. **Artifacts Enable Post-Mortem Learning**

**Every migration produces:**

```
~/.migration-harness/runs/coolstore-20260518-143000/
├─ detect.json               # What was detected (baseline)
├─ graph.json                # Code structure (dependencies)
├─ PLAN.md                   # What we planned to do
├─ execution-log.md          # What actually happened + lessons
├─ verification-report.md    # What errors remain + why
└─ metrics.json              # Quantitative results
```

**Post-migration review meeting:**

1. **Read execution-log.md:**
   - Which steps had lessons? (tribal knowledge gaps)
   - Which patterns did LLM miss? (update reference)

2. **Read verification-report.md:**
   - What errors remain? (architectural decisions needed)
   - What did auto-fix handle? (add to reference if recurring)

3. **Compare metrics.json across migrations:**
   - Is error reduction improving? (references getting better)
   - Is time decreasing? (fewer retry loops)

4. **Update references based on lessons:**
   ```bash
   # Extract all pattern gaps
   grep "Pattern gap" execution-log.md > gaps.txt
   
   # Discuss: Which should go in reference?
   # Update javaee-quarkus.md
   # Re-run migration → measure improvement
   ```

**This is the meta-game:**
> Each migration makes the NEXT migration better

---

## 5. **Containerized for Team Consistency**

**Problem:** "Works on my machine" but breaks in CI/CD

**Solution:**

```dockerfile
FROM ubuntu:24.04
RUN apt-get install -y \
    openjdk-21-jdk maven \           # Java migrations
    python3 graphifyy \               # Code graph
    dotnet-sdk-8.0 \                  # .NET migrations
    goose-cli                         # LLM orchestrator

COPY skill-bundle/ /opt/migration-harness/skill-bundle/
COPY bin/ /opt/migration-harness/bin/

ENTRYPOINT ["migration-harness"]
```

**Usage:**
```bash
# Same command, different machines
docker run migration-harness:latest \
  /workspace/repo "Migrate Java EE to Quarkus"

# Runs on: local, CI/CD, teammate laptop
# Result: Identical environment, reproducible
```

---

# Slide 6: Reality Check & Next Steps

## Setting Realistic Expectations

### What Migration-Harness IS:
```
✅ Automates 80-90% of mechanical transformations
✅ Reduces compilation errors from 247 → 23 (90% reduction)
✅ Saves 15 hours of tedious work per migration
✅ Captures tribal knowledge in references
✅ Learns and improves with each migration
✅ Provides clear lessons where human expertise needed
```

### What Migration-Harness IS NOT:
```
❌ Full automation (still needs human review)
❌ Generates tests (only migrates existing code)
❌ Makes architectural decisions (human approves plan)
❌ Understands your business logic (you inject that knowledge)
❌ Fixes custom framework issues automatically (learns from you)
```

---

## Real-World Results

### CoolStore Migration (Java EE 7 → Quarkus 3)

**Human baseline:**
- Senior engineer: 2-3 days (16-24 hours)
- Outcome: 247 → 0 errors after manual fixes

**Migration-harness (Claude Opus 4.6):**
```
Timeline:
  Detect:   6s     ████
  Plan:     4.6m   ████████████████████████
  [Human review: 5 minutes - reviewed plan, approved]
  Execute:  12.6m  ██████████████████████████████████████████████████
  Verify:   56s    ██████
  Total:    18.2m  (automated) + 5m (human) = 23.2 minutes

Results:
  Errors:     247 → 23 (90.7% reduction)
  Steps:      32/35 succeeded (91.4% success rate)
  Remaining:  23 errors requiring architectural decisions
  
Post-migration manual work:
  - Fix @CompanyAuth pattern (2 hours) → Added to reference
  - Add broadcast config for messaging (15 minutes)
  - Review skipped PaymentProcessor (30 minutes, deferred to Q3)
  Total: 2.75 hours

Total time: 23 minutes (automated) + 2.75 hours (human) = ~3 hours
Savings: 16 hours - 3 hours = 13 hours saved (81% time reduction)
```

**Cost:**
- API calls: $3.50
- Engineer time: 13 hours saved × $100/hr = $1,300
- ROI: 371x

---

## What the Remaining Errors Teach Us

**23 errors remaining (all meaningful):**

```
Category 1: Custom framework patterns (14 errors)
  - @CompanyAuth usage (8 errors)
  - @CompanyCache configuration (4 errors)
  - Custom datasource name format (2 errors)
  
  Learning: Our framework patterns not in reference
  Action: Document in javaee-quarkus.md → next migration won't have these

Category 2: Architectural decisions (6 errors)
  - PaymentProcessor integration strategy
  - Messaging broadcast configuration
  - Database schema version compatibility
  
  Learning: Require human expertise, can't automate
  Action: Add as "decision points" in plan phase

Category 3: Edge cases (3 errors)
  - Deprecated Quarkus API usage (version mismatch)
  - Uncommon annotation combination
  
  Learning: Reference incomplete for edge cases
  Action: Add patterns to reference
```

**Key insight:** Errors aren't failures, they're teaching moments

---

## The Learning Curve

**Migration quality over time:**

```
Migration #1 (CoolStore):
  Errors remaining: 23/247 (9.3%)
  Human work: 2.75 hours
  Lessons learned: 12

  ↓ Update reference with @CompanyAuth, broadcast config
  
Migration #2 (InventoryApp - similar):
  Errors remaining: 8/198 (4.0%)
  Human work: 45 minutes
  Lessons learned: 3 (fewer gaps)
  
  ↓ Update reference with custom datasource patterns
  
Migration #3 (ShipmentService - similar):
  Errors remaining: 2/156 (1.3%)
  Human work: 15 minutes
  Lessons learned: 0 (all patterns covered)

Reference evolution:
  Version 1: Generic Java EE → Quarkus (50 patterns)
  Version 2: + Company framework patterns (8 new patterns)
  Version 3: + Edge cases from migration #1, #2 (5 new patterns)
  Result: 63 patterns → covers 98.7% of our codebase patterns
```

**This is the long-term value:**
> Tribal knowledge becomes organizational knowledge

---

## Limitations (Be Honest)

### 1. **Test Generation**
**Current:** Verifies compilation only  
**Missing:** Doesn't create new tests  
**Impact:** Need manual testing after migration  
**Workaround:** Add test creation to human post-migration checklist  
**Roadmap:** Q3 2026 - Add test generation patterns to references

### 2. **Large File Handling**
**Current:** Files > 5K LOC can hit token limits  
**Missing:** Chunking strategy for large files  
**Impact:** Very large classes might fail  
**Workaround:** Split large files before migration  
**Roadmap:** Q2 2026 - Intelligent file chunking

### 3. **Cross-File Refactoring**
**Current:** Single-file transformations only  
**Missing:** "Extract this to a new service" type refactors  
**Impact:** Can migrate but not improve architecture  
**Philosophy:** Migration ≠ refactoring (separate concerns)

### 4. **Custom Framework Deep Integration**
**Current:** Learns patterns from examples  
**Missing:** Doesn't understand framework internals  
**Impact:** First migration of new framework type needs more human input  
**Workaround:** Document patterns in reference after first migration

---

## Roadmap

### Q2 2026
- [ ] **More references:** Spring Boot 2→3, .NET Framework→Core, React class→hooks
- [ ] **Test pattern generation:** Basic smoke tests from reference examples
- [ ] **Large file chunking:** Split >5K LOC files intelligently
- [ ] **Interactive fix mode:** LLM asks clarifying questions when stuck

### Q3 2026
- [ ] **Multi-model ensemble:** Use different models per step (cheap for simple, powerful for complex)
- [ ] **Learning from corrections:** When human edits plan, capture as pattern
- [ ] **Web UI:** Visual plan editor, diff viewer, progress dashboard

### Q4 2026
- [ ] **CI/CD integration:** GitHub Action, GitLab CI pipeline
- [ ] **Incremental migrations:** Partial repo, branch-based
- [ ] **Cost optimization:** Automatic model routing based on step complexity
- [ ] **Reference marketplace:** Share references across teams/companies

---

## Getting Started

### 1. **Install (5 minutes)**
```bash
git clone https://github.com/your-org/migration-harness
cd migration-harness
./install.sh
migration-harness init
```

### 2. **Pick a Migration Candidate**

Good first migration:
- ✅ Small-medium app (< 100K LOC)
- ✅ Well-understood codebase
- ✅ Similar to example (Java EE, Spring Boot, etc.)
- ✅ Non-critical (can afford to iterate)

Avoid for first migration:
- ❌ Mission-critical payment/auth systems
- ❌ Heavy custom framework usage
- ❌ Massive monoliths (500K+ LOC)

### 3. **Run & Learn**
```bash
migration-harness ~/my-app "Migrate from Java EE to Quarkus"

# After migration:
1. Read execution-log.md (what lessons were learned?)
2. Review remaining errors (what patterns missing?)
3. Update reference (add 2-3 patterns)
4. Try migration #2 (measure improvement)
```

### 4. **Compare Models (Optional)**
```bash
# Try 3 models on same app
for model in claude-opus-4-6 gemini-2.5-pro gpt-4o; do
  migration-harness ~/my-app "..." --model $model --output ~/eval/$model
done

eval-harness --compare ~/eval/*
# See which model best for your migration type
```

---

## Discussion: Team Adoption Strategy

**Proposed rollout:**

**Phase 1: Pilot (2 weeks)**
- Pick 2-3 non-critical apps
- Run migrations with human supervision
- Document lessons learned
- Update references

**Phase 2: Reference Building (1 month)**
- Migrate 5-10 similar apps
- Capture all custom framework patterns
- Build company-specific reference (e.g., `javaee-ourframework-quarkus.md`)
- Measure: error reduction trend

**Phase 3: Production Use (ongoing)**
- Integrate into migration sprints
- Use for new migration projects
- Continuous reference improvement
- Share references across teams

---

## Discussion Questions

1. **Which migrations to prioritize?**
   - Java EE → Quarkus? (How many apps?)
   - Spring Boot 2 → 3? (.NET Framework → Core?)
   - What's the business impact of each?

2. **Reference ownership:**
   - Who maintains javaee-quarkus.md?
   - How do we capture tribal knowledge from seniors?
   - Process for updating after each migration?

3. **Quality bar:**
   - Is 90% error reduction acceptable?
   - Or aim higher with more manual review?
   - What's the right balance?

4. **Model selection:**
   - Always powerful model (Opus/Gemini Pro)?
   - Or tiered (simple migrations use cheaper model)?

5. **Integration with workflow:**
   - Run in CI/CD automatically?
   - Manual trigger per migration sprint?
   - Who approves plans?

---

## Call to Action

**Next Steps:**

1. **This week:** Set up migration-harness (30 minutes)
2. **Next week:** Run pilot migration on test app
3. **Week 3-4:** Capture lessons, update references
4. **Month 2:** Production migration sprint

**Who wants to:**
- Try it on their next migration?
- Help build references for our frameworks?
- Champion this within their team?

---

## Resources

📂 **GitHub:** https://github.com/your-org/migration-harness  
📖 **Guides:**
  - [README.md](README.md) - Overview
  - [DOCKER_E2E_GUIDE.md](DOCKER_E2E_GUIDE.md) - Container setup
  - [CONFIG_GUIDE.md](CONFIG_GUIDE.md) - Model selection
  
🧪 **Eval Framework:** https://github.com/your-org/eval-harness  
💬 **Slack:** #migration-harness  
📧 **Contact:** your-name@company.com

---

**Thank you! Questions?**

---

# APPENDIX: Key Message Reinforcement

## The 3 Core Messages (Repeat Throughout)

### 1. "Humans Decide, LLMs Execute"
- Planning requires architectural thinking (human)
- Execution is mechanical transformation (LLM)
- Both learn from each other (feedback loop)

### 2. "Success = Significant Progress, Not Perfection"
- 247 → 23 errors is a WIN (90% reduction)
- Remaining errors are meaningful (not busywork)
- Focus engineer time on high-value decisions

### 3. "Each Migration Improves the Next"
- Lessons → Reference updates
- Reference updates → Fewer errors next time
- Tribal knowledge → Organizational knowledge

---

## Analogies That Work

**Migration-harness is like:**

**GPS navigation:**
- GPS suggests route (plan)
- You approve or adjust (approval gate)
- GPS handles turn-by-turn (execution)
- You handle unexpected situations (custom patterns)
- You teach GPS about closed roads (update reference)

**Pair programming:**
- LLM is junior pair (fast, tireless, but needs guidance)
- You are senior pair (inject context, make decisions)
- Junior learns your patterns over time (references improve)
- Junior handles repetitive work, you handle complex logic

---

**End of Presentation V2**
