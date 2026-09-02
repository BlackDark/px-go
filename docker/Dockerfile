FROM golang:1.27-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/BlackDark/px-go/internal/version.Version=${VERSION} -X github.com/BlackDark/px-go/internal/version.Commit=${COMMIT} -X github.com/BlackDark/px-go/internal/version.Date=${DATE}" -o /out/px-go ./cmd/px

FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35
COPY --from=builder /out/px-go /px-go
USER nonroot:nonroot
EXPOSE 3128
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/px-go", "--health-check", "--port=3128"]
ENTRYPOINT ["/px-go"]
CMD ["--gateway"]
