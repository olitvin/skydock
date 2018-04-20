FROM crosbymichael/golang

# go get to download all the deps
RUN go get -u github.com/olitvin/skydock

ADD . /go/src/github.com/olitvin/skydock
ADD plugins/ /plugins

RUN cd /go/src/github.com/olitvin/skydock && go install . ./...

ENTRYPOINT ["/go/bin/skydock"]
