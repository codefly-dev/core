FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
ARG BUILD_INPUT=one
RUN CGO_ENABLED=0 go build -ldflags="-X main.buildInput=${BUILD_INPUT}" -o /app/server .
FROM scratch
COPY --from=build /app/server /server
ENTRYPOINT ["/server"]
