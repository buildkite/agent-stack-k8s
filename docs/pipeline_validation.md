# Validating your pipeline

With the unstructured nature of Buildkite plugin specs, it can be
easy to mess up your configuration and then have to debug why your agent pods are failing to start.

To help prevent this, there's a linter that uses [JSON
schema](https://json-schema.org/) to validate the pipeline and plugin
configuration.

This currently can't catch every sort of error and you might still get a reference to a Kubernetes volume that doesn't exist or other errors of that sort, but it will validate that the fields match the API spec we expect.

Our JSON schema can also be used with editors that support JSON Schema by configuring your editor to validate against the schema found [here](./cmd/linter/schema.json).
