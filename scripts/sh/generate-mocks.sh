#!/bin/bash

# Usage:
# ./generate-all-mocks.sh <package1> <interface1> <output1> [<package2> <interface2> <output2> ...]

if ! command -v mockgen >/dev/null 2>&1; then
  echo "mockgen not found. Installing Uber's mockgen..."
  go install github.com/uber/mock/mockgen@latest
  export PATH=$PATH:$(go env GOPATH)/bin
fi

if ! command -v mockgen >/dev/null 2>&1; then
  echo "Error: mockgen still not found after installation. Please check your GOPATH/bin is in PATH."
  exit 1
fi

if [ $(( $# % 3 )) -ne 0 ]; then
  echo "Invalid number of arguments. Each mock requires 3 arguments: <package> <interface> <output-file>"
  echo "Example:"
  echo "  $0 github.com/meu/pkg MeuInterface mocks/meu_mock.go"
  exit 1
fi

while [ "$#" -gt 0 ]; do
  PACKAGE=$1
  INTERFACE=$2
  OUTPUT=$3
  shift 3

  echo "Generating mock for interface '$INTERFACE' in package '$PACKAGE' at '$OUTPUT'"

  mockgen -destination="$OUTPUT" -package=$(basename $(dirname $OUTPUT)) "$PACKAGE" "$INTERFACE"

  if [ $? -ne 0 ]; then
    echo "Failed to generate mock for $INTERFACE"
  fi
done

echo "All mocks processed."
