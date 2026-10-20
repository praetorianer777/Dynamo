#!/usr/bin/env bash
# Makes room on a GitHub-hosted runner for the tools' images built from
# source: the runner has about 14 GB free, and twelve images and a kind
# cluster need more. It removes toolchains the runner image preinstalls and
# Dynamo never uses, so it refuses to run anywhere but such a runner.
set -euo pipefail
[[ "${GITHUB_ACTIONS:-}" == true && "${RUNNER_ENVIRONMENT:-}" == github-hosted ]] \
  || { echo "free-runner-disk.sh deletes system directories, so it runs only on a GitHub-hosted runner." >&2; exit 1; }
df -h / | tail -1
sudo rm -rf /usr/share/dotnet /usr/local/lib/android /opt/ghc /opt/hostedtoolcache/CodeQL /usr/local/share/boost
docker image prune -af > /dev/null
df -h / | tail -1
