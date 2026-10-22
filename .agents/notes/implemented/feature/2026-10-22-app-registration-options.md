# Agent Note: App Registration Options

Status: implemented

## Problem

Registering an App should allow its owner to configure a product callback and
choose when to provision backend credentials. Requiring HTTPS for every callback
rejects HTTP product deployments. Requiring a first Secret during registration
also couples App configuration to credential provisioning even when the product
is not ready to use a key.

## Decision

Registered product callbacks accept absolute HTTP or HTTPS URLs with a host.
They reject user information, fragments, and reserved `code`, `state`, or `error`
query parameters. App registration and both product SDKs preserve the original
callback string, and authorization still requires exact registered membership.
This replaces the earlier HTTPS/explicit-loopback callback restriction recorded
by the [SDK decision](2026-10-13-product-sdks.md). It leaves the gateway public
origin and SDK base-origin HTTPS policy unchanged: HTTP for those origins still
requires the explicit loopback option.

App creation accepts optional `initial_secret` metadata. Omitting the field
creates the App without a credential and returns only `app`. Supplying an object
validates its required name and optional description, then creates the App and
credential atomically and returns `app`, `secret`, and `secret_id`. Supplied
`null` is invalid. The Console offers this issuance choice explicitly and opens
a one-time plaintext display only when creation issued a credential.

The [Secret metadata decision](2026-10-13-secret-metadata.md) retains ownership of
names, descriptions, metadata editing, and migration history. This decision
supersedes only its original requirement to issue a first Secret during App
creation. Owners can issue a named Secret later through the existing credential
endpoint. App credentials remain required for result retrieval; an App with no
active credential cannot authenticate that operation. No authentication bypass
or stored-data migration is introduced.

The [blueprint](../../../../docs/blueprint/README.md) owns the architecture. The
[API reference](../../../../docs/reference/api.md) owns creation responses and
callback validation, the
[configuration reference](../../../../docs/reference/configuration.md) owns the
gateway-origin option, and the [SDK reference](../../../../docs/reference/sdks.md)
owns client URL validation.

## Alternatives

**Keep HTTPS or explicitly enabled loopback HTTP for callbacks.** This was the
prior policy. It prevents registration and SDK use for HTTP product callbacks
outside loopback, so it does not support the required product deployments.

**Always issue a first Secret when registering an App.** This was the original
metadata contract. It produces a credential and a one-time value before an owner
chooses to provision the backend, so App registration now permits deferring that
issuance through the existing Secret endpoint.

## Consequences

App registration supports HTTP product callbacks and credential-free setup.
HTTP callback navigation has no TLS transport protection; choosing that callback
scheme is the App owner's deployment decision. Exact callback registration,
reserved-query validation, product-owned browser state, and authenticated
one-time retrieval remain required.

Creation consumers must distinguish `{app}` from `{app, secret, secret_id}`.
Issuance retains the existing atomic write and one-time disclosure behavior;
metadata reads cannot recover plaintext. An App created without a Secret needs a
later credential before a product backend can retrieve an identity result.
