# Build
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/init ./cmd/init
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/mysql-init ./cmd/init

# Runtime
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/mysql-init /mysql-init
ENTRYPOINT ["/mysql-init"]
