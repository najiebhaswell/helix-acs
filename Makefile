IMAGE_NAME ?= ${PROJECT_NAME}
VERSION ?= 1.0.0

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build           Run build.sh script (uses IMAGE_NAME and VERSION)"
	@echo "  deploy          Run deploy.sh script (uses IMAGE_NAME and VERSION)"
	@echo "  mocks           Run generate-mocks.sh script (pass args as MOCK_ARGS)"
	@echo "  test            Run test.sh script"
	@echo "  setup           Run setup.sh script"
	@echo "  swagger         Run swagger-doc.sh script"
	@echo ""
	@echo "Docker targets:"
	@echo "  docker-up       Start PostgreSQL, MongoDB, Redis with docker-compose"
	@echo "  docker-down     Stop all services"
	@echo "  docker-logs     View logs from all services"
	@echo "  docker-psql     Connect to PostgreSQL shell"
	@echo "  docker-mongo    Connect to MongoDB shell"
	@echo "  docker-redis    Connect to Redis CLI"

.PHONY: build
build:
	@scripts/sh/build.sh $(IMAGE_NAME) $(VERSION)

.PHONY: deploy
deploy:
	@scripts/sh/deploy.sh $(IMAGE_NAME) $(VERSION)

.PHONY: mocks
mocks:
	@scripts/sh/generate-mocks.sh $(MOCK_ARGS)

.PHONY: test
test:
	@scripts/sh/test.sh

.PHONY: swagger
swagger:
	@scripts/sh/swagger.sh

# ============================================================
# Docker targets for PostgreSQL + MongoDB + Redis
# ============================================================

.PHONY: docker-up
docker-up:
	@echo "🚀 Starting Docker services (PostgreSQL, MongoDB, Redis)..."
	@docker-compose up -d
	@echo "⏳ Waiting for services to be ready..."
	@sleep 5
	@docker-compose ps
	@echo "✅ Services started successfully!"
	@echo ""
	@echo "Connection strings:"
	@echo "  PostgreSQL: postgresql://helix:helix_password@localhost:5432/helix_parameters"
	@echo "  MongoDB:    mongodb://helix:helix_password@localhost:27017/helix_acs"
	@echo "  Redis:      redis://localhost:6379"
	@echo ""
	@echo "pgAdmin: http://localhost:5050"

.PHONY: docker-down
docker-down:
	@echo "⛔ Stopping Docker services..."
	@docker-compose down
	@echo "✅ Services stopped"

.PHONY: docker-down-all
docker-down-all:
	@echo "🗑️  Stopping and removing all data..."
	@docker-compose down -v
	@echo "✅ All services and data removed"

.PHONY: docker-logs
docker-logs:
	@docker-compose logs -f

.PHONY: docker-psql
docker-psql:
	@docker-compose exec postgresql psql -U helix -d helix_parameters

.PHONY: docker-mongo
docker-mongo:
	@docker-compose exec mongodb mongosh

.PHONY: docker-redis
docker-redis:
	@docker-compose exec redis redis-cli

.PHONY: docker-ps
docker-ps:
	@docker-compose ps

.PHONY: docker-status
docker-status:
	@echo "🔍 Docker Services Status:"
	@docker-compose ps
	@echo ""
	@echo "PostgreSQL Health:"
	@docker-compose exec -T postgresql pg_isready -U helix -d helix_parameters && echo "✅ PostgreSQL OK" || echo "❌ PostgreSQL DOWN"
	@echo ""
	@echo "MongoDB Health:"
	@docker-compose exec -T mongodb mongosh --eval "db.adminCommand('ping')" > /dev/null 2>&1 && echo "✅ MongoDB OK" || echo "❌ MongoDB DOWN"
	@echo ""
	@echo "Redis Health:"
	@docker-compose exec -T redis redis-cli ping > /dev/null 2>&1 && echo "✅ Redis OK" || echo "❌ Redis DOWN"
