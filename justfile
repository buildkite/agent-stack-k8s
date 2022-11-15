default: lint generate build test

build:
  echo Building…

test:
  go test ./...

lint:
  echo Linting…

generate:
    go run github.com/Khan/genqlient api/genqlient.yaml
