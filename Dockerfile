FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY web/ ./web/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/astromatcher .

FROM alpine:3.20
RUN addgroup -S app && adduser -S app -G app && mkdir -p /app/data && chown -R app:app /app
WORKDIR /app
USER app
COPY --from=build /bin/astromatcher /usr/local/bin/astromatcher
ENV PORT=10000
EXPOSE 10000
CMD ["astromatcher"]
