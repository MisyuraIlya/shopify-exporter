FROM golang:1.25.5-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/sync-to-shopify ./cmd/sync-to-shopify

FROM alpine:3.19
WORKDIR /app
COPY --from=build /out/sync-to-shopify /app/sync-to-shopify
ENTRYPOINT ["/app/sync-to-shopify"]
