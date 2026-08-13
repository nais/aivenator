#!/usr/bin/env bash
# [MISE] description="Update all Go tools to latest"
set -euo pipefail

go list tool | xargs go get -tool
go mod tidy
