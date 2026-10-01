FROM alpine:3

RUN apk add --no-cache ca-certificates

COPY wispjar /usr/local/bin/wispjar

ENTRYPOINT ["wispjar"]
