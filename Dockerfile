FROM golang:alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod/ \
    go mod download -x

COPY . .

ENV GOCACHE=/root/.cache/go-build
RUN --mount=type=cache,target="/root/.cache/go-build" \
    --mount=type=cache,target=/go/pkg/mod/ \
    go build -o api ./cmd/api

FROM alpine

WORKDIR /app

COPY --from=builder /build/api /app/api

CMD ["./api"]