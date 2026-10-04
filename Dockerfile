# syntax=docker/dockerfile:1
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN if [ -f package.json ] && grep -q '"build"' package.json; then npm ci; fi
COPY web/ ./
RUN if grep -q '"build"' package.json; then npm run build; fi

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/lightbacklog ./cmd/lightbacklog

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/lightbacklog /lightbacklog
ENV LB_DATA_DIR=/data LB_ADDR=0.0.0.0:8100
EXPOSE 8100
USER nonroot
ENTRYPOINT ["/lightbacklog"]
