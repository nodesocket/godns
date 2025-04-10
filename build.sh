#!/usr/bin/env bash
set -eo pipefail; [[ $TRACE ]] && set -x

for GOARCH in amd64 arm64; do
  for GOOS in linux darwin; do
    # only support darwin arm64
    if [ "$GOOS" != "darwin" ] || [ "$GOARCH" != "amd64" ]; then
      printf "building... bin/godns_%s_%s\n" $GOOS $GOARCH
      GOARCH=$GOARCH GOOS=$GOOS go build -o bin/godns_${GOOS}_${GOARCH}
    fi
  done
done
