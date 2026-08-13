#!/usr/bin/env bash
# [MISE] description="Run vulncheck"
set -euo pipefail

go tool golang.org/x/vuln/cmd/govulncheck ./...
