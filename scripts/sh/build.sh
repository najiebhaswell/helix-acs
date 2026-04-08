#!/bin/bash

# Usage:
# ./build-image.sh image-name version

if [ "$#" -ne 2 ]; then
  echo "Usage: $0 <image-name> <version>"
  exit 1
fi

IMAGE_NAME=$1
VERSION=$2
TAG="${IMAGE_NAME}:${VERSION}"

echo "Building Docker image with tag: $TAG"

docker build -t "$TAG" -f ../../deploy/docker/Dockerfile ../

if [ $? -eq 0 ]; then
  echo "Image $TAG built successfully!"
else
  echo "Failed to build image $TAG"
  exit 1
fi
