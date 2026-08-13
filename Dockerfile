# ---- build ----
FROM golang:1.24-alpine AS build
WORKDIR /src
# Cache deps first
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/dredge .

# ---- runtime ----
FROM alpine:3.20
ARG VERSION=dev
LABEL org.opencontainers.image.title="dredge" \
      org.opencontainers.image.description="Geführtes, sicheres Aufräumen für Docker" \
      org.opencontainers.image.source="https://github.com/W0rkingChr1s/dredge" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"

# Feste UID/GID: /config wird per bind-mount vom Host gestellt und muss diesem
# Nutzer gehören – ohne festen Wert lässt sich das nicht dokumentieren.
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 10001 -S dredge && \
    adduser -u 10001 -S -G dredge dredge && \
    mkdir -p /config /var/lib/dredge && \
    chown -R dredge:dredge /config /var/lib/dredge

COPY --from=build /out/dredge /usr/local/bin/dredge

# Leere named volumes übernehmen Eigentümer und Rechte dieser Pfade aus dem
# Image – deshalb kann der Dienst in /var/lib/dredge schreiben.
ENV DREDGE_CONFIG=/config/config.yaml
USER dredge:dredge

# Standard: interner Scheduler im Vordergrund.
ENTRYPOINT ["dredge"]
CMD ["daemon"]
