#!/usr/bin/env bash
# =============================================================
# goose-migrate.sh
# Bootstrap script for interactive Goose code migrations
#
# Usage:
#   ./goose-migrate.sh <github-url> <migration-type>
#
# Examples:
#   ./goose-migrate.sh https://github.com/foo/coolstore java-ee-to-quarkus
#   ./goose-migrate.sh https://github.com/foo/app python2-to-python3
#   ./goose-migrate.sh https://github.com/foo/ui react-class-to-hooks
# =============================================================

set -euo pipefail

# ── Colors ────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
RESET='\033[0m'

# ── Helpers ───────────────────────────────────────────────────
info()    { echo -e "${CYAN}ℹ ${RESET}$*"; }
success() { echo -e "${GREEN}✅ ${RESET}$*"; }
warn()    { echo -e "${YELLOW}⚠️  ${RESET}$*"; }
error()   { echo -e "${RED}❌ ${RESET}$*"; exit 1; }
header()  { echo -e "\n${BOLD}${BLUE}$*${RESET}\n"; }

# ── Usage ─────────────────────────────────────────────────────
usage() {
  echo -e "${BOLD}Usage:${RESET}"
  echo "  $0 <github-url> <migration-type>"
  echo ""
  echo -e "${BOLD}Migration types:${RESET}"
  echo "  java-ee-to-quarkus     Java EE 7 (JBoss/WebLogic) → Quarkus 3"
  echo "  python2-to-python3     Python 2 → Python 3"
  echo "  react-class-to-hooks   React class components → functional + hooks"
  echo "  node-upgrade           Node.js version upgrade (14→20)"
  echo ""
  echo -e "${BOLD}Examples:${RESET}"
  echo "  $0 https://github.com/RedHat/coolstore java-ee-to-quarkus"
  echo "  $0 https://github.com/foo/myapp python2-to-python3"
  exit 1
}

# ── Args ──────────────────────────────────────────────────────
[[ $# -lt 2 ]] && usage

REPO_URL="$1"
MIGRATION_TYPE="$2"
REPO_NAME=$(basename "$REPO_URL" .git)
WORK_DIR="$(pwd)/${REPO_NAME}"
SKILLS_DIR="${HOME}/.config/goose/skills"
SKILL_NAME="goose-migration-v2"

# ── Validate migration type ────────────────────────────────────
case "$MIGRATION_TYPE" in
  java-ee-to-quarkus|python2-to-python3|react-class-to-hooks|node-upgrade)
    ;;
  *)
    error "Unknown migration type: '$MIGRATION_TYPE'. Run $0 --help for options."
    ;;
esac

# ── Banner ────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}${BLUE}"
echo "  🪿  Goose Migration Bootstrap"
echo "  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo -e "${RESET}"
echo -e "  Repo:      ${CYAN}${REPO_URL}${RESET}"
echo -e "  Migration: ${CYAN}${MIGRATION_TYPE}${RESET}"
echo -e "  Target:    ${CYAN}${WORK_DIR}${RESET}"
echo ""

# ── Step 1: Check dependencies ────────────────────────────────
header "Step 1/5 — Checking dependencies"

command -v git  >/dev/null 2>&1 || error "git is not installed."
command -v goose >/dev/null 2>&1 || error "goose is not installed. See: https://goose-docs.ai/docs/getting-started/installation"

# Check goose has an API key configured
if ! goose configure --list 2>/dev/null | grep -q "anthropic\|openai\|gemini" 2>/dev/null; then
  warn "Could not verify Goose API key. Make sure 'goose configure' is set up."
fi

success "All dependencies found"

# ── Step 2: Clone repo ────────────────────────────────────────
header "Step 2/5 — Cloning repository"

if [[ -d "$WORK_DIR" ]]; then
  warn "Directory ${WORK_DIR} already exists."
  read -rp "  Delete and re-clone? [y/N] " confirm
  if [[ "$confirm" =~ ^[Yy]$ ]]; then
    rm -rf "$WORK_DIR"
    info "Removed existing directory"
  else
    info "Using existing directory"
  fi
fi

if [[ ! -d "$WORK_DIR" ]]; then
  info "Cloning ${REPO_URL}..."
  git clone "$REPO_URL" "$WORK_DIR" || error "Failed to clone repo. Check the URL and your network."
  success "Cloned to ${WORK_DIR}"
fi

cd "$WORK_DIR"

# ── Step 3: Discover project ──────────────────────────────────
header "Step 3/5 — Discovering project"

# Count files and lines
JAVA_FILES=$(find src/main/java -name "*.java" 2>/dev/null | sort)
JAVA_COUNT=$(echo "$JAVA_FILES" | grep -c ".java" || echo "0")
JAVA_LINES=$(find src/main/java -name "*.java" -exec wc -l {} + 2>/dev/null | tail -1 | awk '{print $1}' || echo "0")
RESOURCE_FILES=$(find src/main/resources -type f 2>/dev/null | sort || echo "")
PKG=$(echo "$JAVA_FILES" | head -1 | sed 's|src/main/java/||' | sed 's|/[^/]*\.java||' | head -1 || echo "com/your/package")

info "Found ${JAVA_COUNT} Java files (${JAVA_LINES} lines)"
info "Package root: ${PKG}"

# Detect what needs migrating
NEEDS_JAVAX=$(grep -rl "javax\." src/main/java 2>/dev/null | wc -l | tr -d ' ')
NEEDS_EJB=$(grep -rl "@Stateless\|@Stateful\|@EJB\|@MessageDriven" src/main/java 2>/dev/null | wc -l | tr -d ' ')
NEEDS_JMS=$(grep -rl "javax.jms\|MessageListener" src/main/java 2>/dev/null | wc -l | tr -d ' ')
NEEDS_WEBLOGIC=$(find src/main/java -path "*/weblogic/*" 2>/dev/null | wc -l | tr -d ' ')

echo ""
echo -e "  ${YELLOW}Files needing javax→jakarta:${RESET} ${NEEDS_JAVAX}"
echo -e "  ${YELLOW}Files with EJB annotations:${RESET}   ${NEEDS_EJB}"
echo -e "  ${YELLOW}Files with JMS/MDB:${RESET}           ${NEEDS_JMS}"
echo -e "  ${YELLOW}WebLogic stubs to delete:${RESET}     ${NEEDS_WEBLOGIC}"
echo ""

# Build ordered file list
build_java_ee_order() {
  local files=()

  # 1. Config files
  [[ -f "pom.xml" ]] && files+=("pom.xml")
  [[ -f "src/main/resources/application.properties" ]] && \
    files+=("src/main/resources/application.properties") || \
    files+=("src/main/resources/application.properties  ← CREATE NEW")

  # 2. Utils
  while IFS= read -r f; do files+=("$f"); done < \
    <(find src/main/java -path "*/utils/*.java" | sort)

  # 3. Persistence
  while IFS= read -r f; do files+=("$f"); done < \
    <(find src/main/java -path "*/persistence/*.java" | sort)

  # 4. Models
  while IFS= read -r f; do files+=("$f"); done < \
    <(find src/main/java -path "*/model/*.java" | sort)

  # 5. Services (MDBs last - hardest)
  while IFS= read -r f; do files+=("$f"); done < \
    <(find src/main/java -path "*/service/*.java" | grep -v "MDB\|Mdb" | sort)
  while IFS= read -r f; do files+=("$f"); done < \
    <(find src/main/java -path "*/service/*MDB.java" -o -path "*/service/*Mdb.java" 2>/dev/null | sort)

  # 6. REST
  while IFS= read -r f; do files+=("$f"); done < \
    <(find src/main/java -path "*/rest/*.java" | sort)

  # 7. Cleanup
  [[ -f "src/main/resources/META-INF/persistence.xml" ]] && \
    files+=("DELETE: src/main/resources/META-INF/persistence.xml")
  [[ -f "src/main/webapp/WEB-INF/beans.xml" ]] && \
    files+=("DELETE: src/main/webapp/WEB-INF/beans.xml")
  [[ -f "src/main/webapp/WEB-INF/web.xml" ]] && \
    files+=("DELETE: src/main/webapp/WEB-INF/web.xml")
  [[ -d "src/main/java/weblogic" ]] && \
    files+=("DELETE: src/main/java/weblogic/ (entire directory)")

  printf '%s\n' "${files[@]}"
}

build_python_order() {
  local files=()
  [[ -f "requirements.txt" ]] && files+=("requirements.txt")
  [[ -f "setup.py" ]] && files+=("setup.py")
  while IFS= read -r f; do files+=("$f"); done < \
    <(find . -name "__init__.py" | grep -v ".tox\|venv\|node_modules" | sort)
  while IFS= read -r f; do files+=("$f"); done < \
    <(find . -name "*.py" | grep -v "__init__\|test_\|.tox\|venv" | sort)
  while IFS= read -r f; do files+=("$f"); done < \
    <(find . -name "test_*.py" | grep -v ".tox\|venv" | sort)
  printf '%s\n' "${files[@]}"
}

build_react_order() {
  local files=()
  [[ -f "package.json" ]] && files+=("package.json")
  while IFS= read -r f; do files+=("$f"); done < \
    <(find src -name "*.jsx" -o -name "*.tsx" 2>/dev/null | \
      xargs grep -l "extends.*Component\|extends.*React.Component" 2>/dev/null | sort)
  printf '%s\n' "${files[@]}"
}

build_node_order() {
  local files=()
  [[ -f "package.json" ]] && files+=("package.json")
  while IFS= read -r f; do files+=("$f"); done < \
    <(find src -name "*.js" -o -name "*.ts" 2>/dev/null | \
      grep -v "node_modules\|test\|spec" | sort)
  printf '%s\n' "${files[@]}"
}

# Build file list
case "$MIGRATION_TYPE" in
  java-ee-to-quarkus)   FILE_LIST=$(build_java_ee_order) ;;
  python2-to-python3)   FILE_LIST=$(build_python_order) ;;
  react-class-to-hooks) FILE_LIST=$(build_react_order) ;;
  node-upgrade)         FILE_LIST=$(build_node_order) ;;
esac
FILE_COUNT=$(echo "$FILE_LIST" | wc -l | tr -d ' ')

success "Discovery complete — ${FILE_COUNT} migration items identified"

# ── Step 4: Show plan and get approval ────────────────────────
header "Step 4/5 — Migration Plan (approval required)"

echo -e "${BOLD}Migration: Java EE 7 → Quarkus 3${RESET}"
echo -e "${BOLD}Files to process: ${FILE_COUNT}${RESET}"
echo ""

i=1
while IFS= read -r f; do
  if [[ "$f" == DELETE:* ]]; then
    echo -e "  ${RED}${i}.${RESET} ${f}"
  else
    echo -e "  ${GREEN}${i}.${RESET} ${f}"
  fi
  ((i++))
done <<< "$FILE_LIST"

echo ""
echo -e "${YELLOW}Review the plan above.${RESET}"
read -rp "  Approve and generate .goosehints? [y/N] " approve
[[ "$approve" =~ ^[Yy]$ ]] || { info "Aborted by user."; exit 0; }

# ── Step 5: Generate .goosehints ──────────────────────────────
header "Step 5/5 — Generating .goosehints"

HINTS_FILE="${WORK_DIR}/.goosehints"

cat > "$HINTS_FILE" << HINTS
# =============================================================
# AUTO-GENERATED by goose-migrate.sh
# Project: ${REPO_NAME}
# Migration: Java EE 7 → Quarkus 3.x
# Generated: $(date)
# Total: ${JAVA_COUNT} Java files (~${JAVA_LINES} LOC)
# =============================================================

## ⚠️ CRITICAL TOKEN RULES — FOLLOW EXACTLY

1. Read ONE file at a time using \`cat <single-path>\` only
2. NEVER use: \`for f in \$(find ...); do cat \$f; done\`
3. NEVER use: \`find . -exec cat {} +\`
4. After writing each migrated file: STOP and wait for user to say "next"
5. Do NOT re-read files you already migrated
6. Do NOT re-print the full plan in each message
7. Do NOT run \`mvn compile\` until user explicitly asks
8. Keep each response focused: read → transform → write → stop

---

## Project Context

- App: ${REPO_NAME}
- Migration: Java EE 7 (JBoss EAP 7.4) → Quarkus 3.x
- Java files: ${JAVA_COUNT} files (~${JAVA_LINES} LOC)
- Base package: $(echo "$PKG" | sed 's|/|.|g')

---

## Migration Order (migrate EXACTLY in this sequence, one at a time)

HINTS

# Write numbered file list
i=1
while IFS= read -r f; do
  echo "${i}. ${f}" >> "$HINTS_FILE"
  ((i++))
done <<< "$FILE_LIST"

cat >> "$HINTS_FILE" << 'HINTS'

---

## Transformation Rules

### Imports
- javax.ejb.*           → REMOVE (replace annotations below)
- javax.inject.*        → jakarta.inject.*
- javax.enterprise.*    → jakarta.enterprise.*
- javax.persistence.*   → jakarta.persistence.*
- javax.ws.rs.*         → jakarta.ws.rs.*
- javax.transaction.*   → jakarta.transaction.*
- javax.json.*          → jakarta.json.*
- javax.xml.bind.*      → jakarta.xml.bind.*
- javax.jms.*           → REMOVE (replace with SmallRye Reactive Messaging)
- weblogic.*            → REMOVE entirely

### Annotations
- @Stateless            → @ApplicationScoped
- @Stateful             → @ApplicationScoped
- @EJB                  → @Inject
- @Local / @Remote      → REMOVE

### MDB Pattern (@MessageDriven → @Incoming)
BEFORE:
  @MessageDriven(activationConfig = { ... })
  public class FooMDB implements MessageListener {
      public void onMessage(Message msg) { ... }
  }

AFTER:
  @ApplicationScoped
  public class FooMDB {
      @Inject Logger log;
      @Incoming("channel-name")
      public void onMessage(String payload) { ... }
  }

Channel name comes from application.properties:
  mp.messaging.incoming.channel-name.connector=smallrye-amqp
  %dev.mp.messaging.incoming.channel-name.connector=smallrye-in-memory

### JMS Sender Pattern (JMSContext → Emitter)
BEFORE:
  @Resource(mappedName="...") Topic topic;
  @Inject JMSContext context;
  context.createProducer().send(topic, json);

AFTER:
  @Inject @Channel("channel-name") Emitter<String> emitter;
  emitter.send(json);

### StartupListener (WebLogic → Quarkus)
BEFORE:
  extends ApplicationLifecycleListener
  public void postStart(ApplicationLifecycleEvent evt) { ... }

AFTER:
  @ApplicationScoped
  void onStart(@Observes StartupEvent ev) { ... }

### pom.xml
- packaging: war → jar
- Remove: javaee-api dependency, maven-war-plugin
- Add: quarkus-bom 3.8.4 in dependencyManagement
- Add: quarkus-maven-plugin
- Required extensions:
  quarkus-arc, quarkus-rest-jackson, quarkus-hibernate-orm,
  quarkus-jdbc-postgresql, quarkus-flyway,
  quarkus-smallrye-reactive-messaging-amqp,
  quarkus-smallrye-health, quarkus-oidc, quarkus-smallrye-openapi

### application.properties (replaces persistence.xml + web.xml)
  quarkus.datasource.db-kind=postgresql
  quarkus.datasource.jdbc.url=jdbc:postgresql://localhost:5432/appdb
  quarkus.datasource.username=user
  quarkus.datasource.password=password
  quarkus.hibernate-orm.database.generation=none
  quarkus.flyway.migrate-at-start=true
  quarkus.http.port=8080

---

HINTS

# Append migration-type-specific verify commands
case "$MIGRATION_TYPE" in
  java-ee-to-quarkus)
    cat >> "$HINTS_FILE" << 'VERIFY'
## After ALL files migrated:
  mvn clean compile 2>&1 | tail -30
Fix errors ONE AT A TIME — show first error only, fix it, stop.
VERIFY
    ;;
  python2-to-python3)
    cat >> "$HINTS_FILE" << 'VERIFY'
## Transformation Rules (Python 2 → 3)
- print "x"        → print("x")
- xrange(...)      → range(...)
- unicode(x)       → str(x)
- raw_input(...)   → input(...)
- except E, e:     → except E as e:
- dict.iteritems() → dict.items()
- / int division   → // where integer needed

## After ALL files migrated:
  python3 -m py_compile <file>
  python3 -m pytest tests/ -x -q 2>&1 | head -30
VERIFY
    ;;
  react-class-to-hooks)
    cat >> "$HINTS_FILE" << 'VERIFY'
## Transformation Rules (React class → hooks)
- class Foo extends Component → const Foo = () =>
- this.state = { x }         → const [x, setX] = useState(...)
- this.setState({ x: val })  → setX(val)
- componentDidMount          → useEffect(() => {...}, [])
- componentWillUnmount       → useEffect(() => { return cleanup }, [])
- this.props.x               → props.x or destructure

## After ALL files migrated:
  npm run build 2>&1 | tail -30
VERIFY
    ;;
  node-upgrade)
    cat >> "$HINTS_FILE" << 'VERIFY'
## Transformation Rules (Node upgrade)
- Buffer(size)     → Buffer.alloc(size)
- url.parse()      → new URL()
- createCipher     → createCipheriv

## After ALL files migrated:
  node --check src/*.js 2>&1 | head -20
VERIFY
    ;;
esac

success ".goosehints written to ${HINTS_FILE}"

# ── Install/verify skill ──────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -f "${SCRIPT_DIR}/${SKILL_NAME}.skill" ]]; then
  mkdir -p "$SKILLS_DIR"
  cp "${SCRIPT_DIR}/${SKILL_NAME}.skill" "$SKILLS_DIR/"
  success "Skill installed to ${SKILLS_DIR}"
elif [[ -d "${SKILLS_DIR}/${SKILL_NAME}" ]]; then
  success "Skill already installed at ${SKILLS_DIR}/${SKILL_NAME}"
else
  warn "Skill file not found. Goose will still work using .goosehints only."
fi

# ── Launch Goose ─────────────────────────────────────────────
echo ""
echo -e "${BOLD}${GREEN}═══════════════════════════════════════════${RESET}"
echo -e "${BOLD}${GREEN}  🎉 Ready! Launching Goose session...${RESET}"
echo -e "${BOLD}${GREEN}═══════════════════════════════════════════${RESET}"
echo ""
echo -e "${YELLOW}When Goose starts, it will auto-paste the first prompt.${RESET}"
echo -e "${YELLOW}After each file, type: ${BOLD}next${RESET}"
echo -e "${YELLOW}To compile and check:  ${BOLD}compile${RESET}"
echo -e "${YELLOW}To quit:               ${BOLD}Ctrl+C${RESET}"
echo ""

# Write the session starter prompt to a temp file
PROMPT_FILE=$(mktemp /tmp/goose-start-XXXXXX.txt)
cat > "$PROMPT_FILE" << 'PROMPT'
Read .goosehints only. Do NOT read any source files yet.
Confirm you understand the migration order, then migrate item #1 only. Stop after.
PROMPT

cd "$WORK_DIR"

# Launch goose - pipe the initial prompt then hand off to interactive mode
{
  cat "$PROMPT_FILE"
  echo ""
  cat  # hand off stdin to user
} | goose session

rm -f "$PROMPT_FILE"
