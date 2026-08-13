#!/usr/bin/env bash
# [MISE] description="Verify golangci-lint linter configuration"
set -euo pipefail

go tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint config verify
