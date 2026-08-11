#!/bin/sh
set -eu

example_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

wingman exec \
  --schema "$example_dir/project.schema.json" \
  "Inspect this repository and return its metadata" \
  | tee "$example_dir/project.json"

printf '\nStructured result saved to %s\n' "$example_dir/project.json" >&2
