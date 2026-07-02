---
name: no-javax-imports
description: Enforces that no javax.* imports remain after migration.
---

# No javax Imports

This is a rule (always-loaded). After migrating to Jakarta EE, no
`javax.*` imports should remain in the codebase.

## Rule

Every Java source file must use `jakarta.*` packages instead of
`javax.*`. If you find a `javax.*` import, replace it with the
corresponding `jakarta.*` equivalent.

Common replacements:
- `javax.servlet.*` -> `jakarta.servlet.*`
- `javax.persistence.*` -> `jakarta.persistence.*`
- `javax.inject.*` -> `jakarta.inject.*`
- `javax.annotation.*` -> `jakarta.annotation.*`
- `javax.ws.rs.*` -> `jakarta.ws.rs.*`
