default: lint generate build test

build:
  echo Building…

test:
  echo Testing…

lint:
  echo Linting…

generate:
    go run github.com/Khan/genqlient api/genqlient.yaml
