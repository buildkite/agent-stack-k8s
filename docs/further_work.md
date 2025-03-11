
# Open questions

- How to deal with stuck jobs? Timeouts?
- How to deal with pod failures (not job failures)?
  - Report failure to Buildkite from controller?
  - Emit pod logs to Buildkite? If agent isn't starting correctly
  - Retry?