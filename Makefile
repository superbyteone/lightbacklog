# Everything runs in containers: the host needs only Docker with the Compose plugin.
PKGS = ./cmd/... ./internal/... ./migrations/... ./web/...
UID := $(shell id -u)
GID := $(shell id -g)

.PHONY: test fmt build up down logs
test:
	mkdir -p .cache/go .cache/gomod
	docker run --rm -u "$(UID):$(GID)" -e HOME=/tmp -e GOCACHE=/src/.cache/go -e GOMODCACHE=/src/.cache/gomod \
	  -e CGO_ENABLED=0 -v "$(PWD)":/src -w /src golang:1.26-alpine sh -c 'go vet $(PKGS) && go test $(PKGS)'
fmt:
	mkdir -p .cache/go .cache/gomod
	docker run --rm -u "$(UID):$(GID)" -e HOME=/tmp -e GOCACHE=/src/.cache/go -e GOMODCACHE=/src/.cache/gomod \
	  -v "$(PWD)":/src -w /src golang:1.26-alpine gofmt -w cmd internal migrations web
build:
	LB_UID=$(UID) LB_GID=$(GID) docker compose build
up:
	mkdir -p data backups
	LB_UID=$(UID) LB_GID=$(GID) docker compose up -d --build
down:
	docker compose down
logs:
	docker compose logs -f --tail=100
