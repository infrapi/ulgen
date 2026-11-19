# thanos, built from source. No configuration inside: mount one at
# /etc/thanos/thanos.yaml.
#
#   podman build -t thanos .
#   podman run -v ./thanos.yaml:/etc/thanos/thanos.yaml:ro,z -p 8080:8080 thanos
FROM docker.io/library/golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /thanos ./cmd/thanos

FROM docker.io/library/alpine:3.22
RUN apk add --no-cache ca-certificates curl \
 && adduser -D -H -u 10001 thanos
COPY --from=build /thanos /usr/local/bin/thanos
USER thanos
EXPOSE 8080
ENTRYPOINT ["thanos"]
CMD ["-config", "/etc/thanos/thanos.yaml"]
