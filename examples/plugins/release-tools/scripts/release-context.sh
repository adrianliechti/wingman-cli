#!/bin/sh
set -eu

# Plain stdout from SessionStart becomes additional agent context. Wingman
# supplies both variables to every plugin hook command.
printf 'Release tooling is available. Changelog template: %s. Persistent plugin data: %s.\n' \
  "$PLUGIN_ROOT/templates/changelog.md" \
  "$PLUGIN_DATA"
