FROM golang:1.26-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/BlackDark/px-go/internal/version.Version=${VERSION} -X github.com/BlackDark/px-go/internal/version.Commit=${COMMIT} -X github.com/BlackDark/px-go/internal/version.Date=${DATE}" -o /out/px-go ./cmd/px

FROM gcr.io/distroless/static-debian12:nonroot@sha256:b7bb25d9f7c31d2bdd1982feb4dafcaf137703c7075dbe2febb41c24212b946f
COPY --from=builder /out/px-go /px-go
USER nonroot:nonroot
EXPOSE 3128
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/px-go", "--health-check", "--port=3128"]
ENTRYPOINT ["/px-go"]
CMD ["--gateway"]
