# Validating your pipeline

Buildkite plugin specifications are unstructured by nature, which can lead to configuration errors that cause agent pod startup failures. These issues can be difficult and time-consuming to troubleshoot.

To prevent configuration problems before deployment, we suggest using a linter that uses [JSON Schema](https://json-schema.org/) to validate your pipeline and plugin configurations.

This linter currently can't catch every sort of error and you might still get a reference to a Kubernetes volume that doesn't exist or other similar errors. However, it will validate that the fields match the API spec we expect.

Our JSON schema can also be used with editors that support JSON Schema by configuring your editor to validate against the schema found [here](./cmd/linter/schema.json).
