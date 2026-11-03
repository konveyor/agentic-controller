#!/bin/sh
# Module proxy HTTP/2 failures have interrupted otherwise healthy queue runs.
# Warm the module cache separately so builds and tests themselves run once.
set -eu

for attempt in 1 2 3; do
    if go mod download "$@"; then
        exit 0
    else
        status=$?
    fi

    if [ "$attempt" -eq 3 ]; then
        echo "Go module download failed after ${attempt} attempts" >&2
        exit "$status"
    fi

    delay=$((attempt * 5))
    echo "Go module download failed (exit ${status}); retrying in ${delay}s (${attempt}/3)" >&2
    sleep "$delay"
done
