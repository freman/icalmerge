FROM golang:1.26 AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o icalmerge .

FROM gcr.io/distroless/static-debian12
COPY --from=builder /build/icalmerge /icalmerge
ENTRYPOINT ["/icalmerge", "serve"]
