## Summary

<!-- What changes, and why? Link the phase/issue. -->

Closes #

## Specification

<!-- Restate the requirement before the solution. -->

- Goal:
- Acceptance criteria:
- Edge cases considered:
- Constraints / non-goals:

## Design and SOLID

<!-- Responsibilities, interfaces, dependency direction, alternatives rejected. -->

- Responsibilities touched:
- Interfaces / dependency direction:
- Architectural trade-offs:
- Unverified assumptions:

## TDD evidence (RED -> GREEN)

- [ ] Tests were written before the implementation.
- [ ] The new tests failed first for the expected reason.
- [ ] Tests were not weakened, deleted, or skipped to make the suite pass.

Red (command and relevant failure):

```text

```

Green (command and result):

```text

```

## Validation

- [ ] `make check` passes (fmt, vet, lint, unit, race, build).
- [ ] Relevant integration tests pass (`make test-integration`, Python gRPC suite, or explain why not).
- [ ] `go mod tidy -diff` is clean (if dependencies changed).
- [ ] Documentation updated in the same pull request (if behaviour, configuration or API changed).
- [ ] Complete `git diff` reviewed; no debug code, dead code or unrelated refactoring.

Commands run and results:

```text

```

## Risk and rollout

<!-- Blast radius, compatibility, rollback plan. -->
