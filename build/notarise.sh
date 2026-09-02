#!/usr/bin/env bash
#
# Submits one artefact to Apple's notary service, and RETRIES when Apple is
# the thing that failed.
#
# A release makes two submissions now — the .app and the disk image wrapping
# it — so it is twice as exposed to a service that is demonstrably unreliable.
# The first run of the disk-image step met a bare `504 Gateway Timeout` from
# Apple's front door, fifteen seconds after a submission of the same
# credentials had succeeded; and on 2026-08-28 three submissions sat "In
# Progress" for over ten hours while Apple's status page reported the service
# Available. A pipeline that turns either of those into a failed release is
# not reporting a problem with the release.
#
# A REJECTION IS NOT RETRIED, and telling the two apart is the whole reason
# this is a script rather than a loop inline. `notarytool` exits non-zero both
# when Apple cannot be reached and when Apple has looked at the binary and
# said no — and retrying the second wastes half an hour a go to arrive at the
# same answer, while burying the reason under three copies of itself.
#
# Usage:
#   API_KEY_PATH=… API_KEY_ID=… API_ISSUER_ID=… build/notarise.sh <artefact>

set -euo pipefail

artefact="${1:?usage: notarise.sh <artefact>}"

# Three, with a short backoff. Long enough to ride out a gateway blip or a
# brief queue, short enough that a genuine rejection — which the check below
# should catch anyway — costs minutes rather than hours.
attempts="${NOTARY_ATTEMPTS:-3}"
backoff="${NOTARY_BACKOFF_SECONDS:-60}"

for attempt in $(seq 1 "$attempts"); do
  # --wait blocks until Apple has a verdict, so a rejection fails here rather
  # than shipping something that passes every other check.
  #
  # --timeout IS NOT OPTIONAL. Without it, --wait would hold a macOS runner
  # until GitHub's six-hour job limit killed it — and macOS minutes bill at
  # ten times the Linux rate, so an Apple backlog would quietly cost hours of
  # runner time per release attempt and report as a timeout with no
  # explanation.
  if output=$(xcrun notarytool submit "$artefact" \
    --key "$API_KEY_PATH" \
    --key-id "$API_KEY_ID" \
    --issuer "$API_ISSUER_ID" \
    --wait --timeout 30m 2>&1); then
    echo "$output"
    exit 0
  fi

  echo "$output"

  # Apple looked at it and said no. Another half hour will not change that,
  # and repeating it three times buries the reason.
  if grep -qiE 'status:[[:space:]]*(Invalid|Rejected)' <<<"$output"; then
    echo "notarise: Apple rejected ${artefact}; not retrying." >&2
    exit 1
  fi

  if [ "$attempt" -lt "$attempts" ]; then
    delay=$((backoff * attempt))
    echo "notarise: attempt ${attempt}/${attempts} did not reach a verdict; retrying in ${delay}s." >&2
    sleep "$delay"
  fi
done

echo "notarise: ${artefact} did not notarise after ${attempts} attempts." >&2
exit 1
