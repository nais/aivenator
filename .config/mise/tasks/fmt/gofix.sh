#!/usr/bin/env bash
# [MISE] description="Fix runs the Go fix tool (cmd/fix) on the named packages and applies suggested fixes."
set -euo pipefail

go fix ./...
