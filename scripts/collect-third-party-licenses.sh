#!/usr/bin/env sh
# SPDX-License-Identifier: AGPL-3.0-only

set -eu

output_dir=${1:?usage: collect-third-party-licenses.sh OUTPUT_DIR}
mkdir -p "$output_dir/modules"
: > "$output_dir/MODULES.txt"

go list -deps -f '{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}' ./cmd/... \
  | awk -F '|' 'NF == 3 && $1 != "github.com/netriun/nexus" { print }' \
  | sort -u \
  | while IFS='|' read -r module_name module_version module_dir; do
      safe_name=$(printf '%s@%s' "$module_name" "$module_version" | tr '/:@+' '_____')
      destination="$output_dir/modules/$safe_name"
      mkdir -p "$destination"

      license_files=$(find "$module_dir" -maxdepth 1 -type f \
        \( -iname 'LICENSE' -o -iname 'LICENSE.*' -o -iname 'COPYING' \
        -o -iname 'COPYING.*' -o -iname 'NOTICE' -o -iname 'NOTICE.*' \) \
        -print | sort)
      if [ -z "$license_files" ]; then
        echo "missing root license text for $module_name $module_version" >&2
        exit 1
      fi

      printf '%s %s\n' "$module_name" "$module_version" >> "$output_dir/MODULES.txt"
      for license_file in $license_files; do
        cp "$license_file" "$destination/$(basename "$license_file")"
      done
    done

cp LICENSE LICENSE.md NOTICE "$output_dir/"
