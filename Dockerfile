FROM golang:1.25.5-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/sync-to-shopify ./cmd/sync-to-shopify
# send-test-report proves the SMTP settings in the env file without running a sync:
#   docker run --rm --env-file <env> --entrypoint /app/send-test-report <image>
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/send-test-report ./cmd/send-test-report

FROM alpine:3.19
WORKDIR /app
# ca-certificates is required for the TLS handshake with the SMTP relay; without it
# the report fails with an unknown-authority error. The zoneinfo that REPORT_TIMEZONE
# needs is embedded in the binaries via the time/tzdata import, not taken from the OS.
RUN apk add --no-cache ca-certificates
COPY --from=build /out/sync-to-shopify /app/sync-to-shopify
COPY --from=build /out/send-test-report /app/send-test-report
ENTRYPOINT ["/app/sync-to-shopify"]
