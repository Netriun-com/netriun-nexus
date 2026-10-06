IMAGE_REPO ?= ghcr.io/netriun-com
IMAGE_NAME ?= netriun-nexus
IMAGE_TAG ?= dev
REVISION ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
IMAGES_DIR ?= .dev/images
TRIVY_CACHE ?= .dev/trivy-cache

.PHONY: run dev-deps build test vet image image-check image-save image-scan up down

# Run the Go process on the host while PostgreSQL and Redis stay in containers.
# LOCAL_PORT, POSTGRES_PORT and REDIS_PORT may be overridden when their defaults
# are already in use. RUN_DATABASE_URL/RUN_REDIS_URL can target external services.
run: dev-deps
	@set -a; . ./.env; set +a; \
	docker compose stop app >/dev/null 2>&1 || true; \
	local_port="$${LOCAL_PORT:-8080}"; \
	db_port="$${POSTGRES_PORT:-15432}"; \
	redis_port="$${REDIS_PORT:-16379}"; \
	DATABASE_URL="$${RUN_DATABASE_URL:-postgres://ccmp:$${POSTGRES_PASSWORD}@127.0.0.1:$${db_port}/ccmp?sslmode=disable}" \
	REDIS_URL="$${RUN_REDIS_URL:-redis://:$${REDIS_PASSWORD}@127.0.0.1:$${redis_port}/0}" \
	HTTP_ADDR=":$${local_port}" \
	APP_ORIGIN="http://localhost:$${local_port}" \
	COOKIE_SECURE=false \
	go run ./cmd/nexus
dev-deps:
	docker compose up -d postgres redis
build:
	go build -trimpath -o bin/nexus ./cmd/nexus
test:
	go test -race ./...
vet:
	go vet ./...
image:
	docker build --build-arg VERSION=$(IMAGE_TAG) --build-arg REVISION=$(REVISION) -t $(IMAGE_REPO)/$(IMAGE_NAME):$(IMAGE_TAG) .
image-check: image
	docker image inspect $(IMAGE_REPO)/$(IMAGE_NAME):$(IMAGE_TAG) --format '{{.Config.User}}' | grep -Eq '^nexus$$|^[1-9][0-9]*(:[1-9][0-9]*)?$$'
	docker image inspect $(IMAGE_REPO)/$(IMAGE_NAME):$(IMAGE_TAG) --format '{{json .Config.Entrypoint}}' | grep -F '["nexus"]'
image-save:
	mkdir -p $(IMAGES_DIR)
	docker save -o $(IMAGES_DIR)/$(IMAGE_NAME).tar $(IMAGE_REPO)/$(IMAGE_NAME):$(IMAGE_TAG)
image-scan:
	mkdir -p $(TRIVY_CACHE)
	IMAGES_DIR=$(abspath $(IMAGES_DIR)) TRIVY_CACHE=$(abspath $(TRIVY_CACHE)) docker compose -f deploy/docker/compose.trivy.yaml run --rm --user "$$(id -u):$$(id -g)" trivy image --input /images/$(IMAGE_NAME).tar --scanners vuln --severity HIGH,CRITICAL --exit-code 1 --no-progress
up:
	docker compose up --build -d
down:
	docker compose down
