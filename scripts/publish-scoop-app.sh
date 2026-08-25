#!/usr/bin/env bash
#
# Render the Scoop manifest for the Wingman Agent desktop app and push it to
# the existing bucket. Invoked by GoReleaser after the Windows app archive has
# been built and uploaded.
#
# The CLI remains the `wingman` Scoop package; this publishes the separate
# `wingman-app` GUI package with a Start Menu shortcut and no PATH shim.
#
# Requires: GITHUB_TOKEN (write access to the bucket) — the same token
# GoReleaser uses for the release.
set -euo pipefail

VERSION="${1:?usage: publish-scoop-app.sh <version>}"

# No-op on snapshot / dry-run builds.
case "$VERSION" in
  *SNAPSHOT* | *snapshot* | *-dirty)
    echo "publish-scoop-app: snapshot build ($VERSION), skipping bucket update"
    exit 0
    ;;
esac

BUCKET_OWNER="adrianliechti"
BUCKET_REPO="scoop-bucket"
APP_NAME="wingman-app"
EXE_NAME="Wingman Agent.exe"
REPO_URL="https://github.com/adrianliechti/wingman-agent"
ARCHIVE_NAME="${APP_NAME}_${VERSION}_Windows_x86_64.zip"
ARCHIVE="${SCOOP_ARCHIVE:-dist/app/${ARCHIVE_NAME}}"

if [ ! -f "$ARCHIVE" ]; then
  echo "publish-scoop-app: archive not found: $ARCHIVE" >&2
  exit 1
fi

SHA256="$(shasum -a 256 "$ARCHIVE" | awk '{print $1}')"
echo "publish-scoop-app: ${APP_NAME} ${VERSION} sha256=${SHA256}" >&2

render_manifest() {
  cat <<EOF
{
  "version": "${VERSION}",
  "description": "AI-powered coding assistant desktop app",
  "homepage": "${REPO_URL}",
  "license": "MIT",
  "architecture": {
    "64bit": {
      "url": "${REPO_URL}/releases/download/v${VERSION}/${ARCHIVE_NAME}",
      "hash": "${SHA256}"
    }
  },
  "shortcuts": [
    ["${EXE_NAME}", "Wingman Agent"]
  ]
}
EOF
}

# SCOOP_DRY_RUN=1 renders the manifest to stdout and exits — no clone/push.
if [ "${SCOOP_DRY_RUN:-0}" = "1" ]; then
  render_manifest
  exit 0
fi

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required to push the manifest to the Scoop bucket}"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT
git clone --depth 1 \
  "https://x-access-token:${GITHUB_TOKEN}@github.com/${BUCKET_OWNER}/${BUCKET_REPO}.git" \
  "$WORKDIR/bucket" >/dev/null 2>&1

MANIFEST_FILE="$WORKDIR/bucket/${APP_NAME}.json"
render_manifest > "$MANIFEST_FILE"

cd "$WORKDIR/bucket"

# Stage first, then diff the index — `git diff` alone ignores new files.
git add "${APP_NAME}.json"
if git diff --cached --quiet; then
  echo "publish-scoop-app: manifest already up to date, nothing to push"
  exit 0
fi

git \
  -c user.name="Adrian Liechti" \
  -c user.email="adrian@localhost" \
  commit -m "${APP_NAME} ${VERSION}" >/dev/null
git push origin HEAD >/dev/null 2>&1

echo "publish-scoop-app: pushed ${APP_NAME} ${VERSION} to ${BUCKET_OWNER}/${BUCKET_REPO}"
