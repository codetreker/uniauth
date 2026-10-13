export type CallbackErrorCode =
  | "invalid_callback"
  | "state_mismatch"
  | "provider_denied"
  | "provider_failed"
  | "capacity_exceeded";

export class CallbackError extends Error {
  override readonly name = "CallbackError";

  constructor(readonly code: CallbackErrorCode) {
    super(
      code === "state_mismatch"
        ? "The callback state does not match the pending login."
        : code === "invalid_callback"
          ? "The authentication callback is invalid."
          : "Authentication did not complete.",
    );
  }
}

export class APIError extends Error {
  override readonly name = "APIError";

  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly requestID: string | null,
  ) {
    super(message);
  }
}

export class ProtocolError extends Error {
  override readonly name = "ProtocolError";

  constructor(
    message: string,
    readonly status: number,
    readonly requestID: string | null,
  ) {
    super(message);
  }
}
