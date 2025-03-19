# Securing the stack

> Works for v0.13.0 and later.

If you want to:
- enforce the podSpec used for all jobs at the controller
level and
- prevent users from setting or overriding that podSpec (or various
other parameters), you can use `prohibit-kubernetes-plugin`.

This can be done by either setting a controller flag or within the config `values.yaml`:

```yaml
# values.yaml
...
config:
  prohibit-kubernetes-plugin: true
  pod-spec-patch:
    # Override the default podSpec here.
  ...
```

With `prohibit-kubernetes-plugin` enabled, any job containing the kubernetes
plugin will fail.
