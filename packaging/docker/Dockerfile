FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN go build -o /out/loramapr-receiverd ./cmd/loramapr-receiverd

FROM alpine:3.20
RUN addgroup -S loramapr && adduser -S -G loramapr loramapr
USER loramapr
WORKDIR /home/loramapr
COPY --from=build /out/loramapr-receiverd /usr/bin/loramapr-receiverd
EXPOSE 8080
ENTRYPOINT ["/usr/bin/loramapr-receiverd"]
