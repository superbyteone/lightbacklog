# Contributing

Thanks for your interest in LightBacklog.

- **Bugs and ideas:** open an issue. Include the version (`lightbacklog version`), what you did, what you
  expected, and what happened.
- **Pull requests:** please open an issue first to agree on the change. Small fixes (typos, docs, clear bugs)
  can go straight to a pull request.
- **Before you submit:** run `make test` (Go vet and tests in a container) and, for frontend changes,
  build the UI once to make sure it compiles:

  ```bash
  docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD":/src -w /src/web node:22-alpine \
    sh -c 'npm ci && npm run build && npm run check'
  ```

- By submitting a contribution you agree that it is licensed under the project's MIT licence.

Keep changes focused, match the surrounding style, and add or update tests for behaviour you change.
