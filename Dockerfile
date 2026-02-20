FROM golang:1.26-alpine AS builder

ENV CGO_ENABLED=0

WORKDIR /srv

RUN apk add --no-cache --update git bash curl tzdata && \
    cp /usr/share/zoneinfo/Asia/Almaty /etc/localtime && \
    rm -rf /var/cache/apk/*

COPY ./cmd /srv/cmd
COPY ./pkg /srv/pkg

COPY ./go.mod /srv/go.mod
COPY ./go.sum /srv/go.sum
COPY ./main.go /srv/main.go

COPY ./.git/ /srv/.git

RUN \
    export version="$(git describe --tags --long)" && \
    echo "version: $version" && \
    go build -o /go/build/remapjson -ldflags "-X 'main.version=${version}' -s -w" /srv

FROM alpine:3.22 AS base

RUN apk add --no-cache --update tzdata && \
    cp /usr/share/zoneinfo/Asia/Almaty /etc/localtime && \
    rm -rf /var/cache/apk/*

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/Semior001/remapjson"
LABEL maintainer="Semior <ura2178@gmail.com>"

COPY --from=builder /go/build/remapjson /usr/bin/remapjson
COPY --from=base /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=base /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=base /etc/passwd /etc/passwd
COPY --from=base /etc/group /etc/group

ENTRYPOINT ["/usr/bin/remapjson", "server"]
