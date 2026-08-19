# UNTESTED: cmd/vyomm-api is being implemented by the Controller.
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/vyomm-api ./cmd/vyomm-api
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/vyomm-api /vyomm-api
ENTRYPOINT ["/vyomm-api"]
