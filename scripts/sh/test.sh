#!/bin/bash

echo "Running all tests with verbose output and race detector..."

go test -v -race ./...

if [ $? -eq 0 ]; then
  echo "All tests passed successfully!"
else
  echo "Some tests failed."
  exit 1
fi
