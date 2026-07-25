#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
	echo "usage: $0 vX.Y.Z" >&2
	exit 2
fi

version=$1
if ! printf '%s\n' "$version" |
	grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
	echo "invalid release version: $version" >&2
	exit 2
fi
chart_version=${version#v}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/.." && pwd)

sed -i.bak \
	-e "s/^version: .*/version: $chart_version/" \
	-e "s/^appVersion: .*/appVersion: \"$chart_version\"/" \
	"$root/charts/tunlease/Chart.yaml"
sed -i.bak \
	-e "s/^  tag: .*/  tag: $version/" \
	"$root/charts/tunlease/values.yaml"
rm "$root/charts/tunlease/Chart.yaml.bak" "$root/charts/tunlease/values.yaml.bak"
