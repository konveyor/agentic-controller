# Python 2.7 → Python 3.x: Migration Reference

---
name: python2-to-python3
description: Migration patterns for Python 2.7 to Python 3.8+ (print, unicode, imports, xrange)
applies_to:
  manifests:
    requirements_txt: true
    setup_py: true
    pyproject_toml: true
  graph_patterns:
    - "imports contains __future__"
    - "nodes contains print statement"
    - "imports contains urllib2"
    - "imports contains ConfigParser"
---

## Migration Order (Layer Dependency)

Always migrate in this order — dependencies flow upward:

1. **Build config** - setup.py, requirements.txt, pyproject.toml (Python 3.8+)
2. **Package init** - `__init__.py` files (relative imports)
3. **Utilities** - helper modules, shared code
4. **Data models** - classes, schemas, data structures
5. **Business logic** - services, use cases
6. **API/Views** - Flask/Django routes, FastAPI endpoints
7. **Scripts** - CLI tools, migration scripts
8. **Tests** - unittest → pytest (optional upgrade)
9. **Cleanup** - Remove `.pyc` files, `__pycache__` dirs

**Why this order**: Build config enables Python 3 first. Utilities have no deps. Models depend on utilities. Logic depends on models. API depends on logic.

---

## Import/Package Transformations

Many modules were renamed or reorganized in Python 3:

| Old (Python 2) | New (Python 3) | Notes |
|----------------|----------------|-------|
| `import urllib2` | `import urllib.request` | HTTP requests |
| `import urlparse` | `import urllib.parse` | URL parsing |
| `import ConfigParser` | `import configparser` | Lowercase |
| `import Queue` | `import queue` | Lowercase |
| `import SocketServer` | `import socketserver` | Lowercase |
| `from StringIO import StringIO` | `from io import StringIO` | Text I/O |
| `from cStringIO import StringIO` | `from io import BytesIO` | Binary I/O |
| `import cPickle` | `import pickle` | C implementation merged |
| `import __builtin__` | `import builtins` | Built-in namespace |
| `import httplib` | `import http.client` | HTTP client |
| `import Cookie` | `import http.cookies` | HTTP cookies |
| `from itertools import izip` | Built-in `zip()` | Returns iterator now |
| `from itertools import imap` | Built-in `map()` | Returns iterator now |

---

## Syntax Transformations

| Old (Python 2) | New (Python 3) | Notes |
|----------------|----------------|-------|
| `print "hello"` | `print("hello")` | Function, not statement |
| `print x, y` | `print(x, y)` | Function call |
| `print >> sys.stderr, "error"` | `print("error", file=sys.stderr)` | Keyword argument |
| `xrange(100)` | `range(100)` | `range()` now returns iterator |
| `dict.iteritems()` | `dict.items()` | Returns iterator |
| `dict.iterkeys()` | `dict.keys()` | Returns iterator |
| `dict.itervalues()` | `dict.values()` | Returns iterator |
| `dict.has_key(k)` | `k in dict` | Method removed |
| `unicode("text")` | `str("text")` | `str` is Unicode now |
| `u"unicode string"` | `"unicode string"` | Default in Python 3 |
| `basestring` | `str` | `basestring` removed |
| `long(100)` | `int(100)` | `long` removed, `int` is unlimited |
| `raw_input()` | `input()` | `raw_input` removed |
| `input()` (evaluates) | `eval(input())` | Old `input()` was unsafe |
| `execfile("file.py")` | `exec(open("file.py").read())` | `execfile` removed |
| `<> ` | `!=` | `<>` operator removed |

---

## Pattern Catalog

### Pattern 1: Print Statement → Print Function

**BEFORE (Python 2):**
```python
print "Hello, world!"
print "User:", username
print >> sys.stderr, "Error:", error_msg

# Multi-line
print "Line 1"
print "Line 2"
```

**AFTER (Python 3):**
```python
print("Hello, world!")
print("User:", username)
print("Error:", error_msg, file=sys.stderr)

# Multi-line
print("Line 1")
print("Line 2")
```

**Specific changes:**
1. Add parentheses around all print arguments
2. Replace `print >> file, msg` → `print(msg, file=file)`
3. Remove trailing commas (unless intentional for same-line printing)

**Why**: `print` is a function in Python 3, not a statement. This enables better IDE support, type hints, and consistency.

---

### Pattern 2: xrange() → range()

**BEFORE (Python 2):**
```python
# xrange returns iterator (memory efficient)
for i in xrange(1000000):
    process(i)

# range returns list (memory intensive for large ranges)
numbers = range(10)  # [0, 1, 2, ..., 9]
```

**AFTER (Python 3):**
```python
# range now returns iterator (like xrange)
for i in range(1000000):
    process(i)

# To get a list, wrap in list()
numbers = list(range(10))  # [0, 1, 2, ..., 9]
```

**Specific changes:**
1. Replace all `xrange()` → `range()`
2. If you need a list, use `list(range(...))`

**Why**: Python 3's `range()` is memory-efficient (iterator-based), so `xrange` is no longer needed.

---

### Pattern 3: Dictionary Methods (iteritems, iterkeys, itervalues)

**BEFORE (Python 2):**
```python
users = {"alice": 25, "bob": 30}

# Iterate over items (returns iterator)
for name, age in users.iteritems():
    print name, age

# Iterate over keys
for name in users.iterkeys():
    print name

# Check if key exists
if users.has_key("alice"):
    print "Found alice"

# Get list of items
items = users.items()  # Returns list
```

**AFTER (Python 3):**
```python
users = {"alice": 25, "bob": 30}

# Iterate over items (returns view object, iterator-like)
for name, age in users.items():
    print(name, age)

# Iterate over keys
for name in users.keys():
    print(name)

# Check if key exists
if "alice" in users:
    print("Found alice")

# Get list of items (explicit conversion)
items = list(users.items())  # Returns list
```

**Specific changes:**
1. Replace `.iteritems()` → `.items()`
2. Replace `.iterkeys()` → `.keys()` (or just iterate over dict directly)
3. Replace `.itervalues()` → `.values()`
4. Replace `.has_key(k)` → `k in dict`
5. If you need a list, wrap in `list()`

**Why**: Python 3 removed the `iter*` methods because `.items()`, `.keys()`, `.values()` now return memory-efficient views (not lists).

---

### Pattern 4: Unicode Strings (unicode, basestring)

**BEFORE (Python 2):**
```python
# Explicit unicode string
text = unicode("Hello", "utf-8")
name = u"Alice"

# Type checking
if isinstance(value, basestring):  # str or unicode
    print "It's a string"

# Encoding/decoding
data = "hello".encode("utf-8")  # str → bytes
text = data.decode("utf-8")     # bytes → unicode
```

**AFTER (Python 3):**
```python
# All strings are unicode by default
text = str("Hello")  # or just "Hello"
name = "Alice"       # No u"" prefix needed

# Type checking
if isinstance(value, str):  # str is now unicode
    print("It's a string")

# Encoding/decoding
data = "hello".encode("utf-8")  # str → bytes
text = data.decode("utf-8")     # bytes → str
```

**Specific changes:**
1. Remove `u""` prefix (optional, but no longer needed)
2. Replace `unicode()` → `str()`
3. Replace `basestring` → `str`
4. Be careful: `str` in Python 2 is bytes, in Python 3 is unicode

**Why**: Python 3 has unified text (`str`) and binary (`bytes`) types. All strings are Unicode by default.

---

### Pattern 5: urllib2 → urllib.request

**BEFORE (Python 2):**
```python
import urllib2
import urlparse

# Fetch URL
response = urllib2.urlopen("https://api.example.com/data")
data = response.read()

# Parse URL
parsed = urlparse.urlparse("https://example.com/path?query=1")
print parsed.scheme  # "https"
```

**AFTER (Python 3):**
```python
import urllib.request
import urllib.parse

# Fetch URL
response = urllib.request.urlopen("https://api.example.com/data")
data = response.read()

# Parse URL
parsed = urllib.parse.urlparse("https://example.com/path?query=1")
print(parsed.scheme)  # "https"
```

**Specific changes:**
1. Replace `import urllib2` → `import urllib.request`
2. Replace `import urlparse` → `import urllib.parse`
3. Replace `urllib2.urlopen()` → `urllib.request.urlopen()`
4. Replace `urlparse.urlparse()` → `urllib.parse.urlparse()`

**Why**: Python 3 reorganized `urllib` into submodules (`urllib.request`, `urllib.parse`, `urllib.error`).

---

### Pattern 6: input() vs raw_input()

**BEFORE (Python 2):**
```python
# Read string from user (safe)
name = raw_input("Enter name: ")

# Read and evaluate expression (DANGEROUS!)
age = input("Enter age: ")  # If user types "5", returns int(5)
```

**AFTER (Python 3):**
```python
# Read string from user (always returns string)
name = input("Enter name: ")

# Convert to int explicitly
age = int(input("Enter age: "))  # Safe, no auto-eval
```

**Specific changes:**
1. Replace `raw_input()` → `input()`
2. Replace `input()` → `eval(input())` (if you really need eval, which is rare)

**Why**: Python 2's `input()` was dangerous (auto-evaluated code). Python 3's `input()` always returns a string.

---

### Pattern 7: Exception Handling (as syntax)

**BEFORE (Python 2):**
```python
try:
    risky_operation()
except ValueError, e:  # Comma syntax
    print "Error:", e
```

**AFTER (Python 3):**
```python
try:
    risky_operation()
except ValueError as e:  # 'as' syntax
    print("Error:", e)
```

**Specific changes:**
1. Replace `except Exception, e:` → `except Exception as e:`

**Why**: Python 3 requires the `as` keyword. The comma syntax is removed.

---

### Pattern 8: Relative Imports

**BEFORE (Python 2 - implicit relative):**
```python
# In mypackage/submodule.py
from utils import helper  # Searches current package first
```

**AFTER (Python 3 - explicit relative or absolute):**
```python
# Option 1: Explicit relative import
from .utils import helper  # Same package

# Option 2: Absolute import (recommended)
from mypackage.utils import helper
```

**Specific changes:**
1. Replace implicit relative imports with explicit `.` prefix or absolute imports
2. Prefer absolute imports for clarity

**Why**: Python 3 removed implicit relative imports to avoid ambiguity.

---

## Files to DELETE

| Delete this | Replaced by | Reason |
|-------------|-------------|--------|
| `*.pyc` files | Auto-regenerated | Python 3 bytecode incompatible |
| `__pycache__/` dirs | Auto-created | New bytecode cache location |

**Note**: `.pyc` files from Python 2 won't work in Python 3. Delete them before running Python 3.

```bash
find . -name "*.pyc" -delete
find . -type d -name "__pycache__" -exec rm -rf {} +
```

---

## Files to CREATE

No new files required, but update:

| File | Change |
|------|--------|
| `setup.py` | Update `python_requires='>=3.8'` |
| `requirements.txt` | Update package versions (some may need newer versions) |
| `pyproject.toml` | Add `requires-python = ">=3.8"` |

---

## Build File Changes

### setup.py

**BEFORE (Python 2):**
```python
from distutils.core import setup

setup(
    name='mypackage',
    version='1.0',
    py_modules=['mypackage'],
)
```

**AFTER (Python 3):**
```python
from setuptools import setup, find_packages

setup(
    name='mypackage',
    version='1.0',
    packages=find_packages(),
    python_requires='>=3.8',  # Specify Python 3 requirement
    install_requires=[
        'requests>=2.28.0',
    ],
)
```

### requirements.txt

Check for Python 3 compatibility:
```bash
# BEFORE (Python 2)
Django==1.11.29  # Last version supporting Python 2
requests==2.18.4

# AFTER (Python 3)
Django==4.2.0    # Python 3 only
requests==2.31.0
```

---

## Verification Commands

```bash
# 1. Check Python version
python3 --version  # Should show 3.8+

# 2. Run 2to3 tool (automated migration helper)
2to3 -w mypackage/  # Rewrites files in-place (backup first!)

# 3. Check for Python 2 syntax
grep -rn "print " . --include="*.py" | grep -v "print(" | wc -l
# Should be 0 (no print statements without parentheses)

# 4. Check for xrange
grep -rn "xrange" . --include="*.py" | wc -l
# Should be 0

# 5. Check for old-style imports
grep -rn "import urllib2" . --include="*.py" | wc -l
# Should be 0

# 6. Run tests
python3 -m pytest

# 7. Check syntax
python3 -m py_compile mypackage/**/*.py
```

---

## Notes / Gotchas

### 1. **Integer Division**
Python 2: `5 / 2 = 2` (integer division)  
Python 3: `5 / 2 = 2.5` (float division), `5 // 2 = 2` (integer division)

**Fix**: Use `//` for integer division explicitly.

### 2. **`map()`, `filter()`, `zip()` Return Iterators**
Python 2: `map(func, list)` returns a list  
Python 3: `map(func, list)` returns an iterator

**Fix**: Wrap in `list()` if you need a list: `list(map(func, list))`

### 3. **bytes vs str**
In Python 2, `str` is bytes. In Python 3, `str` is unicode and `bytes` is separate.

**Fix**: Be explicit when working with binary data:
```python
# Python 3
data = b"binary data"  # bytes
text = "unicode text"  # str
```

### 4. **round() Behavior Changed**
Python 2: `round(2.5) = 3.0` (rounds away from zero)  
Python 3: `round(2.5) = 2` (rounds to nearest even)

**Fix**: If you need old behavior, use `math.floor(x + 0.5)`.

### 5. **`__future__` Imports**
Many Python 2 codebases use `from __future__ import` for forward compatibility:
```python
from __future__ import print_function  # Enable print() in Python 2
from __future__ import division        # Enable 3.x division in Python 2
from __future__ import unicode_literals  # Make "" strings unicode
```

**Fix**: These are no-ops in Python 3 (but harmless). You can remove them.

### 6. **Automated Tool: 2to3**
Python includes a migration tool:
```bash
2to3 -w mypackage/  # Rewrites files in-place
```

**Caution**: Always back up your code first! The tool is good but not perfect.

### 7. **Type Annotations (Optional Upgrade)**
Python 3.5+ supports type hints:
```python
def greet(name: str) -> str:
    return f"Hello, {name}"
```

This is optional but recommended for new Python 3 code.

### 8. **f-strings (Python 3.6+)**
Python 3.6 added f-strings for formatting:
```python
# OLD (Python 2)
print("User: %s, Age: %d" % (name, age))

# NEW (Python 3.6+)
print(f"User: {name}, Age: {age}")
```

### 9. **async/await (Python 3.5+)**
Python 3 has native async support:
```python
async def fetch_data():
    await asyncio.sleep(1)
    return "data"
```

Not a requirement for migration, but a powerful feature.

### 10. **Pathlib (Python 3.4+)**
Python 3 introduced `pathlib` for file paths:
```python
# OLD
import os
path = os.path.join("dir", "file.txt")

# NEW
from pathlib import Path
path = Path("dir") / "file.txt"
```

---

## Migration Checklist

- [ ] Update `setup.py` / `pyproject.toml` to require Python 3.8+
- [ ] Run `2to3` tool on codebase (backup first)
- [ ] Replace all `print` statements with `print()` functions
- [ ] Replace `xrange()` → `range()`
- [ ] Replace `.iteritems()` → `.items()` (and similar)
- [ ] Replace `urllib2` → `urllib.request`
- [ ] Replace `raw_input()` → `input()`
- [ ] Replace `unicode()` → `str()`, remove `basestring`
- [ ] Update exception handling to use `as` syntax
- [ ] Fix relative imports (add `.` prefix or use absolute)
- [ ] Delete all `.pyc` files and `__pycache__` directories
- [ ] Update dependencies to Python 3-compatible versions
- [ ] Run full test suite with Python 3
- [ ] Check for integer division issues (`/` vs `//`)
- [ ] Verify binary/text handling (`bytes` vs `str`)

---

## Resources

- [Python 3 Porting Guide](https://docs.python.org/3/howto/pyporting.html)
- [2to3 Documentation](https://docs.python.org/3/library/2to3.html)
- [What's New in Python 3](https://docs.python.org/3/whatsnew/3.0.html)
- [Python 3 Statement](https://python3statement.org/)
- [Conservative Python 3 Porting Guide](https://portingguide.readthedocs.io/)
