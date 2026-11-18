#!/bin/bash
# Waits for the vector map deployed with toolbox/minikube-test.sh and
# serves it on http://localhost:$PORT (default 8080)
#
# To open the map on other devices in the local network (e.g. a phone), set
# ADDRESS=0.0.0.0 to listen on all network interfaces.
set -euo pipefail

RELEASE=cinode-maps
PORT="${PORT:-8080}"
ADDRESS="${ADDRESS:-localhost}"

kubectl() {
    minikube kubectl -- "$@"
}

echo "Waiting for the first map upload, full logs of the map generation:"
echo "  minikube kubectl -- logs -f -l app=$RELEASE-vector-map --all-containers --prefix"
echo

until kubectl rollout status "deploy/$RELEASE-vector-viewer" --timeout=1s >/dev/null 2>&1; do
    POD="$(kubectl get pods -l "app=$RELEASE-vector-map" \
        --sort-by=.metadata.creationTimestamp -o name | tail -1)"
    if [ -n "$POD" ]; then
        echo "$(date +%T) $POD: $(kubectl get "$POD" --no-headers | awk '{print $3}')"

        JOB="$(kubectl get "$POD" -o jsonpath='{.metadata.labels.job-name}')"
        FAILED="$(kubectl get job "$JOB" -o jsonpath='{.status.conditions[?(@.type=="Failed")].status}' 2>/dev/null || true)"
        if [ "$FAILED" = True ]; then
            echo "Map generation failed in job $JOB:"
            kubectl logs "$POD" --all-containers --prefix --tail=20
            exit 1
        fi
    fi
    sleep 20
done

echo
echo "Map available at http://localhost:$PORT/"
if [ "$ADDRESS" != localhost ]; then
    for ip in $(hostname -I); do
        case "$ip" in
            *:*) ;;  # skip IPv6
            *) echo "  also at http://$ip:$PORT/" ;;
        esac
    done
fi
# kubectl port-forward exits on "lost connection to pod" errors which happen
# when the browser cancels pending tile requests, restart it until Ctrl+C
while true; do
    minikube kubectl -- port-forward --address "$ADDRESS" "svc/$RELEASE-vector-viewer" "$PORT:80" || true
    echo "Port forwarding stopped, restarting"
    sleep 1
done
