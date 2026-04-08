#!/usr/bin/env bash
set -e

echo "Installing swag CLI..."
go install github.com/swaggo/swag/cmd/swag@latest

echo "Generating Swagger documentation..."
swag init --parseDependency --parseInternal -g cmd/api/main.go

echo "Successfully, output path ./docs"
