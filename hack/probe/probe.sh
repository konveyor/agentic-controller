#!/bin/sh
# Staging-directory execution probe.
#
# Agents that use scripts embedded in a SKILL.md do not execute them from the
# read-only skill mount -- they write the script to a writable directory and run
# it from there. This probe answers the question that approach actually depends
# on: is there a writable directory in this pod that we can execute from, and is
# it outside the git worktree the harness commits and pushes?
#
# Runs INSIDE a pod. Delivered with:
#     kubectl exec -i <pod> -- sh -s < hack/probe/probe.sh
#
# POSIX sh only -- the agent image is ubi-minimal (no bash or awk guaranteed).
# Emits "RESULT key=value" lines. ALWAYS exits 0: the verdict lives in the
# output, not the exit code, so a hostile environment yields evidence rather
# than a dead container.

PROBE_SCHEMA=konveyor.staging-exec-probe/v1

# Directories an agent might plausibly stage a script into, in preference order.
#
# HOME is often "/" in these images (the agent user has no real home), so filter
# it out: "/" is the container root, not a staging directory, and probing it
# produces a meaningless row plus an empty result key.
_raw_staging="${PROBE_STAGING_DIRS:-/tmp /workspace /workspace/.konveyor ${HOME:-}}"
STAGING_DIRS=""
for _d in ${_raw_staging}; do
    [ -n "${_d}" ] || continue
    [ "${_d}" = "/" ] && continue
    case " ${STAGING_DIRS} " in *" ${_d} "*) continue ;; esac
    STAGING_DIRS="${STAGING_DIRS} ${_d}"
done

SKILLS_DIR="${PROBE_SKILLS_DIR:-/opt/skills}"

# ---------------------------------------------------------------- helpers ---

# Collapse to a single line and truncate. Values are diagnostic strings, not
# data -- lossy is fine, multi-line output corrupting the format is not.
clean() {
    tr '\n\r\t' '   ' 2>/dev/null | cut -c1-160
}

emit() {
    printf 'RESULT %s=%s\n' "$1" "$2"
}

emit_raw() {
    printf 'RESULT %s=%s\n' "$1" "$(printf '%s' "$2" | clean)"
}

have() {
    command -v "$1" >/dev/null 2>&1
}

# Is $1 inside a git worktree? Staging there risks the harness committing and
# force-pushing the script to the user's branch.
#
# git is normally present in the agent image, but ubi-minimal does not ship it.
# When it is missing we must NOT default to "yes" -- that would wrongly condemn
# /tmp. Fall back to path containment against the workspace root.
in_worktree() {
    _d=$1
    if have git; then
        _top=$(git -C "$_d" rev-parse --show-toplevel 2>/dev/null)
        [ -n "$_top" ] && return 0
        return 1
    fi
    _ws=${PROBE_WORKSPACE:-/workspace}
    case "$_d" in
        "$_ws" | "$_ws"/*) return 0 ;;
    esac
    return 1
}

# Find the mount covering $1 and echo "<mountpoint>|<opts>|<fstype>|<superopts>".
#
# /proc/self/mountinfo:
#   field 5  mountpoint
#   field 6  per-mount options   <- MS_NOEXEC lives HERE
#   "-" separator, then fstype, source, super options
#
# Both option sets matter, for different flags:
#   noexec is per-mount only. Reading it from the super options is the single
#          easiest way to get this whole question wrong.
#   ro     can come from EITHER. A k8s ImageVolume under CRI-O looks like
#            ... rw,relatime - overlay overlay ro,seclabel,lowerdir=...
#          i.e. per-mount says rw while the superblock says ro -- and writes
#          fail with EROFS. Checking only field 6 reports it as writable.
#
# Longest matching mountpoint wins; on ties the LAST entry wins, because that is
# what the kernel resolves to when several mounts share a mountpoint.
covering_mount() {
    _target=$1
    _best_len=-1
    _best=""
    while IFS= read -r _line; do
        # shellcheck disable=SC2086
        set -- $_line
        _mp=$5
        _opts=$6
        # After the "-" separator: fstype, source, super options.
        _fstype=""
        _superopts=""
        _seen_dash=0
        _after=0
        for _f in "$@"; do
            if [ "$_seen_dash" = 1 ]; then
                _after=$((_after + 1))
                [ "$_after" = 1 ] && _fstype=$_f
                [ "$_after" = 3 ] && { _superopts=$_f; break; }
                continue
            fi
            [ "$_f" = "-" ] && _seen_dash=1
        done

        _match=0
        case "$_mp" in
            /) _match=1 ;;
            *)
                case "$_target" in
                    "$_mp") _match=1 ;;
                    "$_mp"/*) _match=1 ;;
                esac
                ;;
        esac
        [ "$_match" = 1 ] || continue

        _len=${#_mp}
        # >= not > : later entries win ties (overmounts).
        if [ "$_len" -ge "$_best_len" ]; then
            _best_len=$_len
            _best="$_mp|$_opts|$_fstype|$_superopts"
        fi
    done < /proc/self/mountinfo
    [ -n "$_best" ] && printf '%s' "$_best"
}

# Read-only if EITHER the per-mount options or the superblock say so.
mount_is_ro() {
    has_opt "$1" ro || has_opt "$2" ro
}

# Does the option list contain a given flag? Options are comma-separated.
has_opt() {
    case ",$1," in
        *",$2,"*) return 0 ;;
    esac
    return 1
}

# ------------------------------------------------------------ fingerprint ---
#
# Without this, results are uninterpretable: an OpenShift SCC overrides the
# image's USER, so the UID a check ran as is not knowable from the manifest.

emit schema "$PROBE_SCHEMA"
emit uid "$(id -u 2>/dev/null)"
emit gid "$(id -g 2>/dev/null)"
emit groups "$(id -G 2>/dev/null | clean)"
emit_raw kernel "$(uname -srm 2>/dev/null)"
emit hostname "${HOSTNAME:-$(hostname 2>/dev/null)}"
emit cwd "$(pwd 2>/dev/null)"

if [ -r /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release 2>/dev/null
    emit os "${ID:-unknown}-${VERSION_ID:-unknown}"
else
    emit os unknown
fi

# The only SELinux signal reliably available from inside a container. An empty
# value means SELinux is not labelling this process; it does NOT prove SELinux
# is disabled cluster-wide.
if [ -r /proc/self/attr/current ]; then
    emit_raw selinux_label "$(cat /proc/self/attr/current 2>/dev/null)"
else
    emit selinux_label unavailable
fi

# Distinguishes a SKIPPED check from a FAILED one. A missing python3 must never
# be reported as "python cannot execute here".
for _t in sh bash python3 env cp chmod git stat; do
    if have "$_t"; then emit "tool_$_t" yes; else emit "tool_$_t" no; fi
done

# ------------------------------------------------- staging directory probe ---
#
# THE primary question. For each candidate: can we write a script there, mark it
# executable, and execve it -- and if we do, does it land inside the git worktree
# that the harness commits and force-pushes to the user's branch?

BEST_DIR=""
BEST_DIR_SAFE=""
ANY_EXEC=no

for dir in $STAGING_DIRS; do
    key=$(printf '%s' "$dir" | tr -c 'a-zA-Z0-9' '_')

    if [ ! -d "$dir" ]; then
        emit "stage${key}_present" no
        continue
    fi
    emit "stage${key}_present" yes

    mi=$(covering_mount "$dir")
    if [ -n "$mi" ]; then
        mp=$(printf '%s' "$mi" | cut -d'|' -f1)
        opts=$(printf '%s' "$mi" | cut -d'|' -f2)
        fstype=$(printf '%s' "$mi" | cut -d'|' -f3)
        superopts=$(printf '%s' "$mi" | cut -d'|' -f4)
        emit "stage${key}_mount" "$mp"
        emit "stage${key}_opts" "$opts"
        emit "stage${key}_superopts" "$superopts"
        emit "stage${key}_fstype" "$fstype"
        # noexec is per-mount only -- never read it from the super options.
        if has_opt "$opts" noexec; then
            emit "stage${key}_noexec" yes
        else
            emit "stage${key}_noexec" no
        fi
        if mount_is_ro "$opts" "$superopts"; then
            emit "stage${key}_ro" yes
        else
            emit "stage${key}_ro" no
        fi
    else
        emit "stage${key}_mount" unknown
    fi

    f="$dir/.probe-exec-$$"
    if printf '#!/bin/sh\necho PROBE_EXEC_OK\n' > "$f" 2>/dev/null; then
        emit "stage${key}_write" ok
    else
        emit "stage${key}_write" denied
        continue
    fi

    if chmod +x "$f" 2>/dev/null; then
        emit "stage${key}_chmod" ok
    else
        emit "stage${key}_chmod" denied
    fi

    # The capability the agent actually needs.
    if out=$("$f" 2>&1); then
        emit "stage${key}_exec" ok
        ANY_EXEC=yes
    else
        emit "stage${key}_exec" denied
        emit_raw "stage${key}_exec_err" "$out"
    fi

    # Fallback if execve is blocked: an interpreter only READS the file, so
    # noexec does not stop it.
    if out=$(sh "$f" 2>&1); then
        emit "stage${key}_interp" ok
    else
        emit "stage${key}_interp" denied
        emit_raw "stage${key}_interp_err" "$out"
    fi

    # Can a compiled binary run here? Copy a real ELF rather than shipping one:
    # avoids committing a per-architecture blob to the repo.
    src=""
    for cand in /bin/echo /usr/bin/echo /bin/true; do
        [ -x "$cand" ] && { src=$cand; break; }
    done
    if [ -n "$src" ] && cp "$src" "$dir/.probe-bin-$$" 2>/dev/null; then
        chmod +x "$dir/.probe-bin-$$" 2>/dev/null
        if "$dir/.probe-bin-$$" probe >/dev/null 2>&1; then
            emit "stage${key}_binexec" ok
        else
            emit "stage${key}_binexec" denied
        fi
        rm -f "$dir/.probe-bin-$$" 2>/dev/null
    else
        emit "stage${key}_binexec" skipped
    fi

    # Would staging here leak onto the user's branch? The harness commits and
    # force-pushes the worktree, and PR #53 adds an fsnotify watcher that
    # auto-commits mid-run.
    if in_worktree "$dir"; then
        emit "stage${key}_in_worktree" yes
        if have git; then
            emit "stage${key}_git_worktree" \
                "$(git -C "$dir" rev-parse --show-toplevel 2>/dev/null)"
            if git -C "$dir" check-ignore -q "$f" 2>/dev/null; then
                emit "stage${key}_git_ignored" yes
            else
                emit "stage${key}_git_ignored" no
            fi
        else
            emit "stage${key}_git_worktree" "path-containment (git absent)"
        fi
    else
        emit "stage${key}_in_worktree" no
    fi

    rm -f "$f" 2>/dev/null
done

# Pick the recommended staging dir: first that can execute, preferring one that
# is not inside a git worktree.
for dir in $STAGING_DIRS; do
    key=$(printf '%s' "$dir" | tr -c 'a-zA-Z0-9' '_')
    [ -d "$dir" ] || continue
    f="$dir/.probe-pick-$$"
    printf '#!/bin/sh\nexit 0\n' > "$f" 2>/dev/null || continue
    chmod +x "$f" 2>/dev/null
    if "$f" >/dev/null 2>&1; then
        [ -z "$BEST_DIR" ] && BEST_DIR=$dir
        if ! in_worktree "$dir"; then
            [ -z "$BEST_DIR_SAFE" ] && BEST_DIR_SAFE=$dir
        fi
    fi
    rm -f "$f" 2>/dev/null
done

emit staging_exec_any "$ANY_EXEC"
emit staging_best "${BEST_DIR:-none}"
emit staging_best_outside_git "${BEST_DIR_SAFE:-none}"

# ---------------------------------------------------- skill mount (record) ---
#
# No longer decision-driving -- agents stage scripts elsewhere -- but cheap, and
# it tells us whether skills could ever ship executable payloads directly.
# Reading from the mount always works even under noexec, which is what makes
# "cp from the mount, then exec" a valid alternative to inlining script text.

if [ -d "$SKILLS_DIR" ]; then
    emit skills_present yes
    emit_raw skills_list "$(ls -1 "$SKILLS_DIR" 2>/dev/null | tr '\n' ',')"

    for sd in "$SKILLS_DIR"/*; do
        [ -d "$sd" ] || continue
        name=$(basename "$sd")
        skey=$(printf '%s' "$name" | tr -c 'a-zA-Z0-9' '_')

        mi=$(covering_mount "$sd")
        if [ -n "$mi" ]; then
            opts=$(printf '%s' "$mi" | cut -d'|' -f2)
            fstype=$(printf '%s' "$mi" | cut -d'|' -f3)
            superopts=$(printf '%s' "$mi" | cut -d'|' -f4)
            emit "skill_${skey}_opts" "$opts"
            emit "skill_${skey}_superopts" "$superopts"
            emit "skill_${skey}_fstype" "$fstype"
            # A CRI-O ImageVolume reports per-mount rw with a ro superblock, so
            # these two lines legitimately disagree. noexec is per-mount only.
            if has_opt "$opts" noexec; then
                emit "skill_${skey}_noexec" yes
            else
                emit "skill_${skey}_noexec" no
            fi
            if mount_is_ro "$opts" "$superopts"; then
                emit "skill_${skey}_ro" yes
            else
                emit "skill_${skey}_ro" no
            fi
        fi

        # noexec never blocks reads -- confirm, since the copy-then-exec path
        # depends on it.
        if [ -f "$sd/SKILL.md" ] && head -c 1 "$sd/SKILL.md" >/dev/null 2>&1; then
            emit "skill_${skey}_readable" yes
        else
            emit "skill_${skey}_readable" no
        fi

        # Writes here are EXPECTED to fail. EROFS (read-only fs) vs EPERM (not
        # the owner) is a fork in the remediation road, so capture the text.
        if touch "$sd/.probe-write-$$" 2>/dev/null; then
            emit "skill_${skey}_write" ok
            rm -f "$sd/.probe-write-$$" 2>/dev/null
        else
            err=$(touch "$sd/.probe-write-$$" 2>&1)
            emit "skill_${skey}_write" denied
            emit_raw "skill_${skey}_write_err" "$err"
        fi
    done
else
    emit skills_present no
fi

# ------------------------------------------------------------- the verdict ---
#
# Only BLOCKED_NO_EXEC_SURFACE invalidates the write-to-temp-and-run approach.

if [ "$ANY_EXEC" != yes ]; then
    VERDICT=BLOCKED_NO_EXEC_SURFACE
    REMEDIATION="no writable+executable directory found; agents cannot stage scripts anywhere"
elif [ -n "$BEST_DIR_SAFE" ]; then
    VERDICT="OK_USE_${BEST_DIR_SAFE}"
    REMEDIATION="stage scripts in ${BEST_DIR_SAFE} (writable, executable, outside the git worktree)"
else
    VERDICT=OK_BUT_COMMIT_RISK
    REMEDIATION="only exec-capable dirs are inside the git worktree; staging there risks committing scripts to the user branch"
fi

emit verdict "$VERDICT"
emit_raw remediation "$REMEDIATION"

exit 0
