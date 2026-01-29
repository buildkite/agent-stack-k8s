# Vendored Kubernetes JSON Schemas

This directory contains vendored Kubernetes JSON schema definitions from
[yannh/kubernetes-json-schema](https://github.com/yannh/kubernetes-json-schema).

## Why Vendor?

The `values.schema.json` references Kubernetes API types (like `PodSpec`, `EnvVar`, etc.)
for validation. Vendoring these schemas:

- Makes validation work offline without fetching from GitHub
- Ensures consistent behavior regardless of upstream availability
- Provides explicit version control over the schemas

## Current Version

- **Kubernetes version**: v1.35.0
- **Source**: https://github.com/yannh/kubernetes-json-schema

## Updating the Vendored Schema

To update to a new Kubernetes version:

1. Run the update script:

   ```bash
   ./update-k8s-schema.sh 1.36.0
   ```

2. Update references in `values.schema.json`:

   ```bash
   sed -i '' 's/kubernetes-v1.35.0.json/kubernetes-v1.36.0.json/g' ../values.schema.json
   ```

3. Remove the old schema file if no longer needed.

4. Test that helm linting still works:

   ```bash
   helm lint ../
   ```
