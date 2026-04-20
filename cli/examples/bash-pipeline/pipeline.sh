#!/bin/sh
# Worked example: bash data-processing pipeline using the
# magic-prefix protocol so viewers see live progress per file.
#
# Usage:
#   fernsicht run -- ./pipeline.sh
#
# What you see in your terminal:
#   processing chunk 1/20 ...
#   processing chunk 2/20 ...
#   ...
#
# What viewers see in the browser:
#   bar fills 0% → 100% over the run, labeled "data pipeline"

set -eu

TOTAL=20

# `start` lifecycle gives viewers a labeled bar that resets to 0%.
echo '__fernsicht__ start "data pipeline"'

for i in $(seq 1 "$TOTAL"); do
    # Real work would go here (e.g., process a file, hit an API).
    # For demo, just sleep.
    sleep 0.3

    # Tell fernsicht: i out of TOTAL items done. The CLI strips this
    # line from forwarded output so it doesn't clutter your terminal,
    # then sends a tick to the bridge → viewers see the bar advance.
    echo "__fernsicht__ progress $i/$TOTAL chunk"

    # Normal user-facing log line — passes through unchanged.
    echo "processing chunk $i/$TOTAL ..."
done

echo '__fernsicht__ end'
echo "pipeline finished"
