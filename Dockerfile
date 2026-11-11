FROM docker.io/library/golang:1.27-trixie AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY templates ./templates
COPY assets ./assets
RUN go vet ./... && go test ./...
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /slack-invite .

FROM docker.io/library/debian:trixie-slim
# For HTTPS to Slack. Rebuild the image to pick up updates.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /slack-invite /usr/local/bin/slack-invite
# The ECS service (ooni/devops tf/modules/ooniapi_service) sends traffic to
# port 80. nobody can bind it because Docker lets any user bind low ports in
# a container with its own network, as ECS's bridge mode gives it.
ENV LISTEN_ADDR=:80
EXPOSE 80
USER nobody
ENTRYPOINT ["/usr/local/bin/slack-invite"]
