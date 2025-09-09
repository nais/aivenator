#!/bin/sh
dirty=""

if ! test -z "$(git ls-files --exclude-standard --others)"; then
  dirty="-dirty"
fi
echo "$(date "+%Y-%m-%d")-$(git --no-pager log -1 --pretty=%h)${dirty}"
