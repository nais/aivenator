#!/usr/bin/env bash
# [MISE] description="Build aivenator binary"
set -euo pipefail

go build -o bin/aivenator cmd/aivenator/*.go
