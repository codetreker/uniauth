# Agent Note: Supported Operating System for CI Checks

Status: implemented

## Problem

Playwright's pinned browser and system-dependency installer supports specific
operating-system releases. A moving hosted-runner alias can advance beyond that
support and prevent browser checks from starting even when the build and other
tests pass. [Check run 42983318938](https://github.com/codetreker/uniauth/actions/runs/42983318938/job/130398579847)
reached that failure: Playwright 1.58.2 rejected the runner's Ubuntu 26.04 platform.

## Decision

The [Check workflow](../../../../.github/workflows/check.yml) uses the explicit
`ubuntu-24.04` hosted-runner label, supported by the pinned Playwright 1.58.2
dependency. The job still runs dependency setup, build, static checks, all tests,
Chromium installation with system dependencies, and the complete browser suite.
Browser downloads remain inside the workspace's `.tmp/browsers/` directory.

The [validation decision](../testing/2026-10-12-authentication-validation.md)
continues to own coverage, behavioral checks, and resource cleanup. The
[configuration reference](../../../../docs/reference/configuration.md#build-and-validation)
owns the validation commands and CI environment.

## Alternatives

**Keep `ubuntu-latest`.** This follows new runner releases automatically, but
does not coordinate their operating system with the pinned browser installer.
The observed unsupported-platform failure requires a supported explicit label.

## Consequences

Required browser validation can start on a supported operating system. Future
runner updates require checking the pinned Playwright version's compatibility;
the explicit label does not freeze hosted-image package updates or remove any
test requirement.
