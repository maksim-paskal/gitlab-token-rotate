FROM golang:1.26-alpine AS builder
WORKDIR /gitlab-token-rotate
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /go/bin/gitlab-token-rotate ./cmd

FROM alpine:latest
COPY --from=builder /go/bin/gitlab-token-rotate /usr/local/bin/gitlab-token-rotate
USER 32752
ENTRYPOINT [ "/usr/local/bin/gitlab-token-rotate" ]