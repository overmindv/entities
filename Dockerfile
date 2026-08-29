FROM golang:1.26-alpine AS build
ARG GOPROXY
ENV GOPROXY=${GOPROXY:-https://proxy.golang.org,direct}
WORKDIR /src
# parker подтягивается по тегу (v0.1.0) из модульного прокси — см. go.mod / go.sum.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/entities ./cmd/entities

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget && addgroup -S entities && adduser -S entities -G entities
WORKDIR /app
COPY --from=build /out/entities /usr/local/bin/entities
COPY migrations /app/migrations
USER entities
EXPOSE 8080
ENTRYPOINT ["entities"]
