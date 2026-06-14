# Tasks

## Authorization model release flow

- Add stable/candidate release state for published OpenFGA models.
- Keep writing every valid compiled model to OpenFGA as a candidate.
- Add a promotion mechanism that moves candidate to stable without rewriting the model.
- Store stable/candidate fields in a durable controller-owned place, likely a future CRD such as `AuthorizationModelRelease`.
- Build an AuthZen/OpenFGA proxy that selects the OpenFGA authorization model ID from the stable/candidate state.
- Default query traffic to the stable model.
- Allow explicit candidate traffic for tests/canaries.
- Avoid exposing raw OpenFGA model IDs to callers unless necessary.
