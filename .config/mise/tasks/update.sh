#!/usr/bin/env bash
#MISE description="Update all dependencies (root, api module, and examples)"
set -euo pipefail

echo "📦 Updating Go dependencies (root)..."
go get -u ./...
go mod tidy

echo "✅ All dependencies updated"
