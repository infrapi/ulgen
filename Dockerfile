# The ulgen image GoReleaser publishes on every release
# (ghcr.io/infrapi/ulgen, see .goreleaser.yaml): the ulgen binary it has
# just built, for each platform, on Alpine. No configuration inside: mount
# its dotenv at /etc/ulgen/ulgen.env (ulgen.env.example), with
# INFRAPI_APP_LISTEN_ADDRESS="0.0.0.0:9700" to be reached from outside.
#
#   podman run -v ./ulgen.env:/etc/ulgen/ulgen.env:ro,z -p 9700:9700 ghcr.io/infrapi/ulgen
#   podman run --rm ghcr.io/infrapi/ulgen -version
FROM docker.io/library/alpine:3.22

# ca-certificates: Kayra and the APIs over TLS. curl: the healthcheck an
# orchestrator runs, on /apis/<INFRAPI_ULGEN_API>/v1/-/healthz.
RUN apk add --no-cache ca-certificates curl \
 && adduser -D -H -u 10001 ulgen

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/ulgen /usr/local/bin/

ENV INFRAPI_CONFIG_DOTENV_FILE=/etc/ulgen/ulgen.env
USER ulgen
EXPOSE 9700
ENTRYPOINT ["ulgen"]
