FROM golang:1
WORKDIR /go/src/github.com/imagespy/hub-discoverer/
COPY . .
RUN go build

FROM gcr.io/distroless/base
COPY --from=0 /go/src/github.com/imagespy/hub-discoverer/hub-discoverer /hub-discoverer
ENTRYPOINT ["/hub-discoverer"]
