FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO is needed for go-sqlite3; goolm is the pure-Go olm implementation (no libolm needed).
RUN CGO_ENABLED=1 go build -tags goolm -trimpath -ldflags="-s -w" -o /out/matrix-youtube-preview .

FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=build /out/matrix-youtube-preview /usr/local/bin/matrix-youtube-preview
WORKDIR /data
VOLUME /data
ENTRYPOINT ["/usr/local/bin/matrix-youtube-preview"]
CMD ["-config", "/config/config.yaml"]
