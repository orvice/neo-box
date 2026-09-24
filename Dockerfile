FROM golang:1.26 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -ldflags "-X main.serverCommit=${COMMIT}" -o /app/neobox ./cmd/neobox

FROM gcr.io/distroless/static-debian12

COPY --from=builder /app/neobox /app/neobox

ENTRYPOINT ["/app/neobox"]
