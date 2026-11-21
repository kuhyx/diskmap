#!/bin/bash
# Vendors kuhy_credit from utils@kuhy-credit-v0.3.0
# Re-run this script after bumping TAG.
set -euo pipefail
cd "$(dirname "$0")/.."
readonly TAG="kuhy-credit-v0.3.0"
readonly REPO="https://github.com/kuhyx/utils"
readonly SRC="kuhy_credit/web/src"

cache="$(mktemp -d)"
trap 'rm -rf "$cache"' EXIT
git clone --depth 1 --branch "$TAG" "$REPO" "$cache"
mkdir -p web/vendor/kuhy-credit
cp "$cache/$SRC/kuhy-credit.js" "$cache/$SRC/credit-data.js" web/vendor/kuhy-credit/
