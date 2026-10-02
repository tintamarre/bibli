#!/bin/sh
# Bibli — writes app/build-info.json, which the binary reports as its version.
# Run by CI and by every release job that builds a binary, so they all describe
# the same build. Arguments: the version (default: the tag on HEAD, if any) and
# the build date (default: now). The rest comes from BUILD_* variables.
#
# Into app/, not the repository root: version.go embeds it with go:embed, which
# only sees files beside the package. Written at the root it would be ignored
# and the binary would report the committed placeholder as its version.
set -eu

VERSION=${1:-$(git describe --tags --exact-match 2>/dev/null || echo '')}
DATE=${2:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
SHA=${BUILD_SHA:-$(git rev-parse HEAD)}
REPO=${BUILD_REPOSITORY:-}

# CI passes the run's URL in BUILD_PIPELINE_URL; a local build has none, and the
# About screen simply omits the line when it is empty.
PIPELINE_URL=${BUILD_PIPELINE_URL:-}

cat > app/build-info.json <<JSON
{
  "version": "${VERSION}",
  "commit": "${SHA}",
  "commit_short": "$(echo "$SHA" | cut -c1-7)",
  "commit_date": "$(git show -s --format=%cI "$SHA")",
  "branch": "${BUILD_BRANCH:-}",
  "date": "${DATE}",
  "pipeline_url": "${PIPELINE_URL}",
  "repository": "${REPO}"
}
JSON
cat app/build-info.json
