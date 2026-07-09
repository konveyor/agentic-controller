---
name: python2-to-python3
description: >
  Sub-skill of goose-migration. Handles Stage 2 execution for Python 2 → Python 3
  migrations. Triggers ONLY after migration-plan has run and plan is approved.
  Migrates one file per turn, stops after each, waits for "next". Handles
  "compile", "fix", and "status" commands.
---

# Python 2 → Python 3 Execution Sub-Skill

Executes the approved migration plan from `.goosehints`, one file at a time.

---

## Startup Sequence

1. Read `.goosehints` only — nothing else
2. Confirm file count to user
3. Say: `Ready. Migrating item #1 now.`
4. Migrate item #1, stop

---

## Per-File Loop

```
1. cat <file>        ← read current content
2. Apply transforms  ← per rules below
3. Write file        ← write migrated content
4. Update .goosehints checklist
5. Report: "✅ Item #N done: <filename> — <changes>"
6. STOP. Wait for "next".
```

---

## Transformation Rules

```
print "x"             → print("x")
print >> sys.stderr   → print(..., file=sys.stderr)
xrange(n)             → range(n)
unicode(x)            → str(x)
basestring            → str
long                  → int
raw_input(...)        → input(...)
except E, e:          → except E as e:
raise E, msg          → raise E(msg)
dict.iteritems()      → dict.items()
dict.itervalues()     → dict.values()
dict.iterkeys()       → dict.keys()
has_key(x)            → x in dict
/ (integer division)  → // where integer result needed
u"string"             → "string"  (str is unicode in Python 3)
import cPickle        → import pickle
import ConfigParser   → import configparser
import Queue          → import queue
```

---

## Verify Commands

| User types | Action |
|---|---|
| `next` | Migrate next file, stop |
| `compile` | Run `python3 -m py_compile <last-file>`, show result |
| `test` | Run `python3 -m pytest -x -q 2>&1 \| head -30`, show result |
| `fix` | Fix first error in last file, stop |
| `status` | Show checked vs unchecked in .goosehints |
