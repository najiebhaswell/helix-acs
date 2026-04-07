#!/bin/bash

# Usage:
# ./deploy-image.sh image-name version

if [ "$#" -ne 2 ]; then
  echo "Usage: $0 <image-name> <version>"
  exit 1
fi

IMAGE_NAME=$1
VERSION=$2
TAG="${IMAGE_NAME}:${VERSION}"

echo "Pushing Docker image: $TAG"

docker push "$TAG"

if [ $? -eq 0 ]; then
  echo "Image $TAG pushed successfully!"
else
  echo "Failed to push image $TAG"
  exit 1
fi
