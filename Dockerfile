FROM golang:1
WORKDIR /go/src/github.com/imagespy/hub-discoverer/
COPY . .
RUN go build

FROM debian:stable-slim
RUN apt-get update \
  && apt-get install -y ca-certificates \
  && rm -rf /var/lib/apt/lists/*
COPY --from=0 /go/src/github.com/imagespy/hub-discoverer/hub-discoverer /hub-discoverer
USER nobody
ENTRYPOINT ["/hub-discoverer"]
