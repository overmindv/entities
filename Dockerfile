FROM golang:1.25-alpine AS build
ARG GOPROXY
ENV GOPROXY=${GOPROXY:-https://proxy.golang.org,direct}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/entities ./cmd/entities
RUN GOBIN=/out go install -tags="no_clickhouse no_mssql no_mysql no_sqlite3 no_libsql no_ydb no_vertica" github.com/pressly/goose/v3/cmd/goose@v3.26.0

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget && addgroup -S entities && adduser -S entities -G entities && mkdir -p /var/log/entities && chown -R entities:entities /var/log/entities
WORKDIR /app
COPY --from=build /out/entities /usr/local/bin/entities
COPY --from=build /out/goose /usr/local/bin/goose
COPY migrations /app/migrations
USER entities
EXPOSE 8080
ENTRYPOINT ["entities"]
