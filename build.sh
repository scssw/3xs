#!/bin/bash
set -e

echo "== go =="
go version

echo "== install deps =="
apt-get update >/dev/null
apt-get install -y build-essential pkg-config libsqlite3-dev ca-certificates >/dev/null

export CGO_ENABLED=1

echo "== list =="
ls -la

echo "== find main packages under cmd =="
if [ -d ./cmd ]; then
  mains=$(find ./cmd -maxdepth 2 -name '*.go' -print0 | xargs -0 grep -l '^package main' | sed 's#/[^/]*$##' | sort -u || true)
  echo "$mains"

  target=$(echo "$mains" | grep -m1 'x-ui' || true)
  if [ -z "$target" ]; then target=$(echo "$mains" | head -n1 || true); fi

  if [ -n "$target" ]; then
    echo "== building target: $target =="
    go build -o x-ui "$target"
  else
    echo "== no main under cmd, try root =="
    go build -o x-ui .
  fi
else
  echo "== no cmd dir, try root =="
  go build -o x-ui .
fi

echo "== verify (ldd) =="
ldd ./x-ui | head
