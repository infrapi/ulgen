# ulgen, built from source. No configuration inside: mount one at
# /etc/ulgen/ulgen.yaml.
#
#   podman build -t ulgen .
#   podman run -v ./ulgen.yaml:/etc/ulgen/ulgen.yaml:ro,z -p 8080:8080 ulgen
FROM docker.io/library/golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ulgen ./cmd/ulgen

FROM docker.io/library/alpine:3.22
RUN apk add --no-cache ca-certificates curl \
 && adduser -D -H -u 10001 ulgen
COPY --from=build /ulgen /usr/local/bin/ulgen
USER ulgen
EXPOSE 8080
ENTRYPOINT ["ulgen"]
CMD ["-config", "/etc/ulgen/ulgen.yaml"]
