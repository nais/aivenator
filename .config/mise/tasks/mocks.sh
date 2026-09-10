#!/usr/bin/env bash
# [MISE] description="Regenerate mocks using mockery"
set -euo pipefail

go tool github.com/vektra/mockery/v2
