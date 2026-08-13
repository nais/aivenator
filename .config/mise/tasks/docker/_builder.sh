#!/usr/bin/env bash
#MISE hide=true
set -euo pipefail

docker buildx create --name aivenator-builder --node aivenator-builder0
