#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH='' cd -- "$script_dir/.." && pwd)
output=${1:-"$root/_site"}

mkdir -p "$output"
cp "$root/site/index.html" "$root/site/styles.css" "$root/site/site.js" "$output/"
cp "$root/site/og-image.png" "$output/og-image.png"
cp "$root/site/CNAME" "$output/CNAME"
cp "$root/assets/icon.svg" "$output/icon.svg"
touch "$output/.nojekyll"
