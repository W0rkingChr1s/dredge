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
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S janitor && adduser -S -G janitor janitor
COPY --from=build /out/dredge /usr/local/bin/dredge

# Config wird per Volume gemountet; History in einem Daten-Volume.
ENV DREDGE_CONFIG=/config/config.yaml
VOLUME ["/config", "/var/lib/dredge"]
USER janitor

# Standard: interner Scheduler im Vordergrund.
ENTRYPOINT ["dredge"]
CMD ["daemon"]
