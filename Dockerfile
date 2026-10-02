FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/ ./cmd/...

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /usr/local/bin/
COPY config.yaml /etc/gateway/config.yaml
ENV GATEWAY_CONFIG=/etc/gateway/config.yaml
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/gateway"]
