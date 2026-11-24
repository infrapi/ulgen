# ulgen, built from source. No configuration inside: mount one at
# /etc/ulgen/ulgen.env (ulgen.env.example), INFRAPI_APP_LISTEN_ADDRESS="0.0.0.0:9700".
#
#   podman build -t ulgen .
#   podman run -v ./ulgen.env:/etc/ulgen/ulgen.env:ro,z -p 9700:9700 ulgen
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
ENV INFRAPI_CONFIG_DOTENV_FILE=/etc/ulgen/ulgen.env
EXPOSE 9700
ENTRYPOINT ["ulgen"]
