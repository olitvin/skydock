FROM golang:1.10.1-alpine

RUN apk upgrade --update musl \
    && apk add \
       git \
    && rm -rf /var/cache/apk/*
# go get to download all the deps
RUN go get -u -v github.com/olitvin/skydock

ADD . /go/src/github.com/olitvin/skydock
ADD plugins/ /plugins

RUN cd /go/src/github.com/olitvin/skydock && go install -v . ./...

ENTRYPOINT ["/go/bin/skydock"]
