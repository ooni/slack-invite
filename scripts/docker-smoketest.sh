#!/bin/bash
# Start the image and check it answers. Usage: docker-smoketest.sh IMAGE
set -euo pipefail

IMAGE=${1:?usage: $0 IMAGE}
NAME=slack-invite-smoketest-$RANDOM
PORT=$((RANDOM % 10001 + 30000))

cleanup() {
    docker logs "$NAME" || true
    docker rm -f -v "$NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# The sysctl is Docker's default with its own network; set it for podman too
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:80" \
    --sysctl net.ipv4.ip_unprivileged_port_start=0 \
    -e SLACK_WEBHOOK_URL=https://hooks.slack.com/services/T000/B000/SMOKETEST \
    "$IMAGE"

for path in /health /; do
    for _ in $(seq 20); do
        code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT$path" || true)
        [ "$code" = 200 ] && break
        sleep 0.5
    done
    if [ "$code" != 200 ]; then
        echo "smoketest failed: GET $path answered $code"
        exit 1
    fi
    echo "GET $path: 200"
done
