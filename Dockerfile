FROM golang:1.23-alpine AS builder

WORKDIR /go/src/github.com/Max-Sum/quipu

ARG GOPROXY=https://goproxy.io,direct

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN mkdir /output \
 && CGO_ENABLED=0 go build -o /output/ ./cmd/router \
 && CGO_ENABLED=0 go build -o /output/ ./cmd/subscription

RUN ls

# Router Image
FROM scratch AS router

WORKDIR /
COPY --from=builder \
     /output/router /router
ENV LISTEN_PLAIN=""
ENV LISTEN_TLS=":443"
ENV ALLOW_REDIR="true"
ENV ALLOW_PORTS="0-65535"
ENV FINAL_HTTP=""
ENV FINAL_SOCKS=""
ENV FINAL_TLS=""
ENTRYPOINT [ "/router" ]

# Subscription Image
FROM scratch AS sub

WORKDIR /
COPY --from=builder \
     /output/subscription /sub
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

ENTRYPOINT [ "/sub" ]
CMD ["-c", "config.ini"]
