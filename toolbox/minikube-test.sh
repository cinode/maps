#!/bin/bash
# Deploys osm-machinery with locally built images to minikube
#
# Usage: toolbox/minikube-test.sh [preset]
#
# Presets (toolbox/minikube/values-<preset>.yaml):
#   raster            - raster map (PostgreSQL + tile server), the default
#   vector-luxembourg - vector map of Luxembourg, quick test
#   vector-poland     - vector map of Poland, realistic dataset
#
# Images are built from the working tree with the local docker.
#
# Minikube is started if it is not running yet, its size can be set with
# MINIKUBE_MEMORY (default 4g), MINIKUBE_CPUS (4) and MINIKUBE_DISK (40g).
#
# For vector presets the map generation is started right away, use
# toolbox/minikube-view.sh to watch it and to open the map.

set -euo pipefail

cd "$(dirname "$0")/.."

PRESET="${1:-raster}"
VALUES_FILE="toolbox/minikube/values-${PRESET}.yaml"
if [ ! -f "$VALUES_FILE" ]; then
    echo "Unknown preset '$PRESET', available presets:"
    for f in toolbox/minikube/values-*.yaml; do
        f="${f#toolbox/minikube/values-}"
        echo "  ${f%.yaml}"
    done
    exit 1
fi

case "$PRESET" in
    vector*)
        MAP_TYPE=vector
        RELEASE=cinode-maps
        ;;
    *)
        MAP_TYPE=raster
        RELEASE=cinode-maps-tile-server
        ;;
esac

if ! minikube status 2>/dev/null | grep -q "Running"; then
    echo "Starting minikube"
    minikube start \
        --memory="${MINIKUBE_MEMORY:-4g}" \
        --cpus="${MINIKUBE_CPUS:-4}" \
        --disk-size="${MINIKUBE_DISK:-40g}"
fi

kubectl() {
    minikube kubectl -- "$@"
}

if git diff --quiet; then
    TAGNAME="devel-$(git rev-parse --short HEAD)"
else
    TAGNAME="devel-$(git rev-parse --short HEAD)-dirty-$(date +%s)"
fi

image_values() {
    local key="$1" image="$2"
    # Built with the local docker and loaded into minikube, which works
    # regardless of minikube's container runtime
    docker build --load -t "cinode/$image:$TAGNAME" -f "build/docker/Dockerfile.$image" .
    minikube image load "cinode/$image:$TAGNAME"
    IMAGE_ARGS+=(
        --set "$key.image.registry=docker.io"
        --set "$key.image.repository=cinode/$image"
        --set "$key.image.tag=$TAGNAME"
        --set "$key.image.pullPolicy=Never"
    )
}

IMAGE_ARGS=()
if [ "$MAP_TYPE" = vector ]; then
    image_values vectorMap maps-vector-generator
else
    image_values cinodeUpload maps-tile-uploader
    image_values tileServer maps-tile-server
fi

if ! ls helm/osm-machinery/charts/*.tgz >/dev/null 2>&1; then
    helm dependency build ./helm/osm-machinery
fi

helm \
    upgrade --install \
    "$RELEASE" \
    ./helm/osm-machinery \
    --kube-context minikube \
    --values ./helm/osm-machinery/values.yaml \
    --values "$VALUES_FILE" \
    "${IMAGE_ARGS[@]}"

if [ "$MAP_TYPE" = vector ]; then
    JOB="$RELEASE-vector-map-$(date +%Y%m%d-%H%M%S)"
    kubectl create job --from="cronjob/$RELEASE-vector-map" "$JOB"
    echo
    echo "Vector map generation started in job $JOB, to follow it and open the map run:"
    echo "  toolbox/minikube-view.sh"
fi
