#!/bin/sh
set -eu

project_dir=${1:?usage: run.sh PROJECT_DIR [PACKAGE]}
package=${2:-./...}

cd "$project_dir"
go test -cover "$package"
