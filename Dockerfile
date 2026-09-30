FROM golang:1.26.6-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.24
WORKDIR /app
COPY --from=build /out/server /app/server
# app payload (views/, web/, …). .env NEVER enters the image (.dockerignore):
# config arrives at run time — compose injects .env.example then .env through
# env_file, and environment: outranks both. A clean machine without a .env
# still boots on the injected .env.example (§11.19).
COPY . /app/
EXPOSE 8090
CMD ["/app/server"]
