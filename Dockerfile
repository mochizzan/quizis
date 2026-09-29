FROM golang:1.26.6-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.24
WORKDIR /app
COPY --from=build /out/server /app/server
# brings .env if present, plus .env.example
COPY . /app/
# clean machine has no .env: fall back to the template so boot succeeds (§11.19)
RUN [ -f /app/.env ] || cp /app/.env.example /app/.env
EXPOSE 8090
CMD ["/app/server"]
