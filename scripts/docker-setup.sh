#!/bin/bash
# scripts/docker-setup.sh
# Setup script untuk menjalankan PostgreSQL dengan Docker

set -e

echo "🚀 Helix ACS - PostgreSQL Docker Setup"
echo "======================================"

# Check Docker
if ! command -v docker &> /dev/null; then
    echo "❌ Docker not found. Please install Docker first."
    exit 1
fi

if ! command -v docker-compose &> /dev/null; then
    echo "❌ Docker Compose not found. Please install Docker Compose first."
    exit 1
fi

# Navigate to script directory
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

cd "$PROJECT_ROOT"

echo "📁 Project directory: $PROJECT_ROOT"

# Create necessary directories
echo "📦 Creating directories..."
mkdir -p scripts

# Check if docker-compose.yml exists
if [ ! -f "docker-compose.yml" ]; then
    echo "❌ docker-compose.yml not found"
    exit 1
fi

# Start Docker containers
echo "🐳 Starting Docker containers..."
docker-compose up -d

# Wait for services to be ready
echo "⏳ Waiting for services to start..."
sleep 5

# Check PostgreSQL
echo "🔍 Checking PostgreSQL..."
max_attempts=30
attempt=1
until docker-compose exec -T postgresql pg_isready -U helix -d helix_parameters > /dev/null 2>&1; do
    if [ $attempt -ge $max_attempts ]; then
        echo "❌ PostgreSQL failed to start"
        docker-compose logs postgresql
        exit 1
    fi
    echo "   Attempt $attempt/$max_attempts..."
    sleep 2
    ((attempt++))
done
echo "✅ PostgreSQL is ready"

# Check MongoDB
echo "🔍 Checking MongoDB..."
attempt=1
until docker-compose exec -T mongodb mongosh --eval "db.adminCommand('ping')" > /dev/null 2>&1; do
    if [ $attempt -ge $max_attempts ]; then
        echo "❌ MongoDB failed to start"
        docker-compose logs mongodb
        exit 1
    fi
    echo "   Attempt $attempt/$max_attempts..."
    sleep 2
    ((attempt++))
done
echo "✅ MongoDB is ready"

# Check Redis
echo "🔍 Checking Redis..."
attempt=1
until docker-compose exec -T redis redis-cli ping > /dev/null 2>&1; do
    if [ $attempt -ge $max_attempts ]; then
        echo "❌ Redis failed to start"
        docker-compose logs redis
        exit 1
    fi
    echo "   Attempt $attempt/$max_attempts..."
    sleep 2
    ((attempt++))
done
echo "✅ Redis is ready"

echo ""
echo "✨ All services started successfully!"
echo ""
echo "📊 Services:"
echo "  - PostgreSQL:  localhost:5432 (user: helix, password: helix_password)"
echo "  - MongoDB:     localhost:27017 (user: helix, password: helix_password)"
echo "  - Redis:       localhost:6379"
echo "  - pgAdmin:     http://localhost:5050"
echo ""
echo "🛠️  Useful commands:"
echo "  - View logs:           docker-compose logs -f [service]"
echo "  - PostgreSQL shell:    docker-compose exec postgresql psql -U helix -d helix_parameters"
echo "  - MongoDB shell:       docker-compose exec mongodb mongosh"
echo "  - Redis CLI:           docker-compose exec redis redis-cli"
echo "  - Stop services:       docker-compose down"
echo "  - Stop & remove data:  docker-compose down -v"
echo ""
