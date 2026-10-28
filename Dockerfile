FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/usdt-payout ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H payout
WORKDIR /app
COPY --from=build /out/usdt-payout /usr/local/bin/usdt-payout
RUN mkdir -p /app/data && chown -R payout:payout /app
USER payout
EXPOSE 8088
ENTRYPOINT ["usdt-payout"]
