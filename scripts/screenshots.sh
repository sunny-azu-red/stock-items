#!/usr/bin/env bash
# Retakes docs/screenshots: every page and form, on desktop and phone, in
# light and dark (docs/screenshots/<desktop|mobile>/<light|dark>/<page>.png).
# The README shows four of them by path, so it follows along.
# Usage: scripts/screenshots.sh     (needs what the e2e tests need: DB_* or
# .env, Node with e2e/node_modules, and vips to recompress the PNGs)
# It runs the e2e suite on the laptop-sized window ("laptop", 1360px: the
# desktop shots) and the phone; the screenshot test comes late in it, so the
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

# The sidebar shows a pinned version and date, not the real ones: it looks
# like a commit and a real day, but a re-run then changes only the screenshots
# of the pages that changed.
version=6b1ea22
date="2026-11-02T16:00:00+02:00"
echo "==> e2e tests with screenshots (version $version, date $date)"
(cd e2e && SCREENSHOT_DIR="$shots" SCREENSHOT_VERSION="$version" SCREENSHOT_DATE="$date" npx playwright test --project=laptop --project=mobile)

for d in desktop/light desktop/dark mobile/light mobile/dark; do
  [ -n "$(ls "$shots/$d" 2>/dev/null)" ] || { echo "No screenshots in $d"; exit 1; }
done

echo "==> docs/screenshots"
rm -rf docs/screenshots/desktop docs/screenshots/mobile
mkdir -p docs/screenshots
cp -r "$shots/desktop" "$shots/mobile" docs/screenshots/

# Lossless: the same pixels, about a third smaller. Chromium's anti-aliasing
# varies between runs: by a level or two anywhere, and by up to about ten on
# a few dozen pixels of a rounded corner or an icon's edge. A screenshot that
# differs from the committed one by no more than that keeps the committed
# file. A real change is far more: text is 100+ levels off its background,
# and even a faint border or outline is hundreds of pixels.
echo "==> recompress"
find docs/screenshots -name '*.png' -print0 | while IFS= read -r -d '' f; do
  vips copy "$f" "$shots/png.png[compression=9,strip]"
  mv "$shots/png.png" "$f"
  if git cat-file -e "HEAD:$f" 2>/dev/null && ! git diff --quiet -- "$f"; then
    git show "HEAD:$f" > "$shots/old.png"
    w=$(vipsheader -f width "$f") h=$(vipsheader -f height "$f")
    if [ "${w}x$h" = "$(vipsheader -f width "$shots/old.png")x$(vipsheader -f height "$shots/old.png")" ]; then
      vips subtract "$f" "$shots/old.png" "$shots/d.v"
      vips abs "$shots/d.v" "$shots/a.v"
      max=$(vips max "$shots/a.v" | cut -d. -f1)
      if [ "$max" -le 2 ]; then
        git checkout -q -- "$f"
      elif [ "$max" -le 16 ]; then
        # The pixels off by 3 levels or more, in any colour: fewer than 100?
        vips relational_const "$shots/a.v" "$shots/m.v" moreeq 3
        vips bandbool "$shots/m.v" "$shots/o.v" or
        if awk -v avg="$(vips avg "$shots/o.v")" -v n="$((w * h))" 'BEGIN { exit !(avg * n / 255 < 100) }'; then
          git checkout -q -- "$f"
        fi
      fi
    fi
  fi
done

echo "$(find docs/screenshots -name '*.png' | wc -l) screenshots, $(git status --porcelain -- docs/screenshots | wc -l) changed:"
git status --short -- docs/screenshots | head -20
