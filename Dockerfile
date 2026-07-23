FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ironhide ./cmd/ironhide
RUN GOBIN=/out go install github.com/pressly/goose/v3/cmd/goose@v3.26.0

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget && addgroup -S ironhide && adduser -S ironhide -G ironhide && mkdir -p /var/log/ironhide && chown -R ironhide:ironhide /var/log/ironhide
WORKDIR /app
COPY --from=build /out/ironhide /usr/local/bin/ironhide
COPY --from=build /out/goose /usr/local/bin/goose
COPY migrations /app/migrations
USER ironhide
EXPOSE 8080
ENTRYPOINT ["ironhide"]
