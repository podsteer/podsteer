#!/bin/bash
#
# Badge generator for a private repository.
#
# Shields.io cannot read a private repository's tags, so the version badges are
# static URLs rewritten into README.md whenever a tag is pushed. See
# .github/workflows/update-version-badges.yaml.
#
# Usage: ./build/generate-badges.sh

set -euo pipefail

OUTPUT_DIR="build/badges"
mkdir -p "$OUTPUT_DIR"

# Tag tiers. Production is the plain vX.Y.Z form; the other two are its
# candidates, and must be excluded from it.
PROD_TAG=$(git tag -l 'v*' | grep -v -- '-dev-' | grep -v -- '-rc-' | sort -V | tail -1 || echo "")
RC_TAG=$(git tag -l 'v*-rc-*' | sort -V | tail -1 || echo "")
DEV_TAG=$(git tag -l 'v*-dev-*' | sort -V | tail -1 || echo "")

# Builds a shields.io static badge URL.
create_badge_url() {
    local label="$1"
    local version="$2"
    local color="$3"
    local default_message="$4"

    if [ -n "$version" ]; then
        # shields.io treats a single dash as its label/message separator, so
        # every literal dash in a version has to be doubled.
        encoded_version=$(echo "$version" | sed 's/-/--/g' | sed 's/ /%20/g')
        echo "https://img.shields.io/badge/$label-$encoded_version-$color"
    else
        echo "https://img.shields.io/badge/$label-$default_message-lightgrey"
    fi
}

PROD_BADGE=$(create_badge_url "Production" "$PROD_TAG" "green" "Not%20Released")
RC_BADGE=$(create_badge_url "Staging" "$RC_TAG" "orange" "No%20Candidate")
DEV_BADGE=$(create_badge_url "Development" "$DEV_TAG" "blue" "No%20Dev%20Release")

echo "Production  Badge: $PROD_BADGE"
echo "Staging     Badge: $RC_BADGE"
echo "Development Badge: $DEV_BADGE"

{
    echo "$PROD_BADGE" > "$OUTPUT_DIR/production.txt"
    echo "$RC_BADGE" > "$OUTPUT_DIR/staging.txt"
    echo "$DEV_BADGE" > "$OUTPUT_DIR/development.txt"
    echo "$PROD_TAG" > "$OUTPUT_DIR/production-version.txt"
    echo "$RC_TAG" > "$OUTPUT_DIR/staging-version.txt"
    echo "$DEV_TAG" > "$OUTPUT_DIR/development-version.txt"
}

echo "Badge files saved to $OUTPUT_DIR/"

if [ -f "README.md" ]; then
    sed -i.tmp "s|!\[Production\](https://img.shields.io/badge/Production-[^)]*)|![Production]($PROD_BADGE)|g" README.md
    sed -i.tmp "s|!\[Staging\](https://img.shields.io/badge/Staging-[^)]*)|![Staging]($RC_BADGE)|g" README.md
    sed -i.tmp "s|!\[Development\](https://img.shields.io/badge/Development-[^)]*)|![Development]($DEV_BADGE)|g" README.md
    rm -f README.md.tmp

    echo "README.md updated."
    echo "  Production : ${PROD_TAG:-none}"
    echo "  Staging    : ${RC_TAG:-none}"
    echo "  Development: ${DEV_TAG:-none}"
fi
