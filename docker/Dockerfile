FROM golang:1.26-alpine AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/BlackDark/px-go/internal/version.Version=${VERSION} -X github.com/BlackDark/px-go/internal/version.Commit=${COMMIT} -X github.com/BlackDark/px-go/internal/version.Date=${DATE}" -o /out/px ./cmd/px

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/px /px
USER nonroot:nonroot
EXPOSE 3128
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/px", "--health-check", "--port=3128"]
ENTRYPOINT ["/px"]
CMD ["--gateway"]
