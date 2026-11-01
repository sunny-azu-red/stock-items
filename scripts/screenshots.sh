#!/usr/bin/env bash
# Retakes docs/screenshots: every page and form, on desktop and phone, in
# light and dark (docs/screenshots/<desktop|mobile>/<light|dark>/<page>.png).
# The README shows four of them by path, so it follows along.
# Usage: scripts/screenshots.sh     (needs what the e2e tests need: DB_* or
# .env, Node with e2e/node_modules, and vips to recompress the PNGs)
# It runs the whole e2e suite; the screenshot test comes late in it, so the
# pages have photos, history and trash to show.
set -euo pipefail
cd "$(dirname "$0")/.."

# The screenshots must show committed code.
if [ -n "$(git status --porcelain -- . ':!docs/screenshots')" ]; then
  echo "Commit your changes first: the screenshots must show committed code."
  git status --short -- . ':!docs/screenshots'
  exit 1
fi
command -v vips >/dev/null || { echo "vips not found: sudo apt install libvips-tools"; exit 1; }
[ -d e2e/node_modules ] || (cd e2e && npm ci && npx playwright install chromium)

shots=$(mktemp -d)
trap 'rm -rf "$shots"' EXIT

# The sidebar shows a pinned version, not a real one: it looks like a commit,
# but a commit then changes only the screenshots of the pages it changed.
version=6b1ea22
echo "==> e2e tests with screenshots (version $version)"
(cd e2e && SCREENSHOT_DIR="$shots" SCREENSHOT_VERSION="$version" npx playwright test)

for d in desktop/light desktop/dark mobile/light mobile/dark; do
  [ -n "$(ls "$shots/$d" 2>/dev/null)" ] || { echo "No screenshots in $d"; exit 1; }
done

echo "==> docs/screenshots"
rm -rf docs/screenshots/desktop docs/screenshots/mobile
mkdir -p docs/screenshots
cp -r "$shots/desktop" "$shots/mobile" docs/screenshots/

# Lossless: the same pixels, about a third smaller. Chromium's anti-aliasing
# varies by a level or two between runs: a screenshot that differs from the
# committed one by no more than that keeps the committed file.
echo "==> recompress"
find docs/screenshots -name '*.png' -print0 | while IFS= read -r -d '' f; do
  vips copy "$f" "$shots/png.png[compression=9,strip]"
  mv "$shots/png.png" "$f"
  if git cat-file -e "HEAD:$f" 2>/dev/null && ! git diff --quiet -- "$f"; then
    git show "HEAD:$f" > "$shots/old.png"
    if [ "$(vipsheader -f width "$f")x$(vipsheader -f height "$f")" = \
         "$(vipsheader -f width "$shots/old.png")x$(vipsheader -f height "$shots/old.png")" ]; then
      vips subtract "$f" "$shots/old.png" "$shots/d.v"
      vips abs "$shots/d.v" "$shots/a.v"
      if [ "$(vips max "$shots/a.v" | cut -d. -f1)" -le 2 ]; then
        git checkout -q -- "$f"
      fi
    fi
  fi
done

echo "$(find docs/screenshots -name '*.png' | wc -l) screenshots, $(git status --porcelain -- docs/screenshots | wc -l) changed:"
git status --short -- docs/screenshots | head -20
