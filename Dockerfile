FROM golang:1.23-alpine AS builder

WORKDIR /go/src/github.com/Max-Sum/quipa

ARG GOPROXY=https://goproxy.io,direct

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build ./cmd/router \
 && CGO_ENABLED=0 go build ./cmd/subscription

RUN ls

# Router Image
FROM scratch as router

WORKDIR /
COPY --from=builder \
     /go/src/github.com/Max-Sum/quipa/router /router
ENV LISTEN_PLAIN ""
ENV LISTEN_TLS ":443"
ENV ALLOW_REDIR "true"
ENV ALLOW_PORTS "443"
ENV FINAL_HTTP ""
ENV FINAL_SOCKS ""
ENV FINAL_TLS ""
ENTRYPOINT [ "/router" ]

# Subscription Image
FROM scratch as sub

WORKDIR /
COPY --from=builder \
     /go/src/github.com/Max-Sum/fcbreak/subscription /sub
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

ENTRYPOINT [ "/sub" ]
CMD ["-c", "config.ini"]
