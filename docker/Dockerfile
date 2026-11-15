FROM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/BlackDark/px-go/internal/version.Version=${VERSION} -X github.com/BlackDark/px-go/internal/version.Commit=${COMMIT} -X github.com/BlackDark/px-go/internal/version.Date=${DATE}" -o /out/px-go ./cmd/px

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=builder /out/px-go /px-go
USER nonroot:nonroot
EXPOSE 3128
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/px-go", "--health-check", "--port=3128"]
ENTRYPOINT ["/px-go"]
CMD ["--gateway"]
