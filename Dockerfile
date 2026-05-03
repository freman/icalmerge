FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -o icalmerge .

FROM gcr.io/distroless/static-debian12
COPY --from=builder /build/icalmerge /icalmerge
ENTRYPOINT ["/icalmerge"]
CMD ["serve"]
