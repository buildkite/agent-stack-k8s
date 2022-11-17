default: lint generate build test

build:
  echo Building…

test *FLAGS:
  go test {{FLAGS}} ./...

lint:
  echo Linting…

generate:
    go run github.com/Khan/genqlient api/genqlient.yaml
