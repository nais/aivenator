#!/usr/bin/env bash
# [MISE] description="Format go code using gofumpt"
set -euo pipefail

go tool mvdan.cc/gofumpt -w ./
