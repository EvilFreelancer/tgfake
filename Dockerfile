# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" -o /out/tgfake ./cmd/tgfake

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=build /out/tgfake /usr/local/bin/tgfake
USER 65532:65532
EXPOSE 18790
# Arguments after the image name are added to these, and a later --addr wins.
ENTRYPOINT ["tgfake", "--addr", "0.0.0.0:18790"]
