FROM golang:1.12 AS build

# kube-client doesn't support "go mod" yet, so we need to use dep

RUN curl https://raw.githubusercontent.com/golang/dep/master/install.sh | sh

WORKDIR /go/src/app
COPY Gopkg.toml Gopkg.lock ./
RUN dep ensure -vendor-only

# compile source code

COPY main.go .
RUN go build -o /go/bin/acme-kong-kube-helper

# build final image distroless for size
FROM gcr.io/distroless/base
COPY --from=build /go/bin/acme-kong-kube-helper /acme-kong-kube-helper
CMD ["/acme-kong-kube-helper"]
