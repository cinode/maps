#!/bin/bash

set -euo pipefail

cd "$(dirname "$0")/.."

if ! minikube status | grep -q "Running"; then
    echo "Minikube is not running"
    exit 1
fi

eval "$(minikube docker-env)"

if git diff --quiet; then
    TAGNAME="devel-$(git rev-parse --short HEAD)"
else
    TAGNAME="devel-$(git rev-parse --short HEAD)-dirty-$(date +%s)"
fi

for image in \
    maps-tile-uploader \
    maps-tile-server \
; do
    docker build -t cinode/$image:$TAGNAME -f build/docker/Dockerfile.${image} .
done

VALUES_CONTENT="---
cinodeUpload:
    image:
        tag: $TAGNAME
        registry: docker.io
        repository: cinode/maps-tile-uploader
        pullPolicy: Never
tileServer:
    image:
        tag: $TAGNAME
        registry: docker.io
        repository: cinode/maps-tile-server
        pullPolicy: Never
"

helm \
    upgrade --install \
    cinode-maps-tile-server \
    ./helm/osm-machinery \
    --kube-context minikube \
    --values ./helm/osm-machinery/values.yaml \
    --values <( echo "$VALUES_CONTENT" )
