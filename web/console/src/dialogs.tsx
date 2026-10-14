import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { APIError, secretInputSchema } from "./api";
import type {
  Application,
  AppInput,
  CreateAppInput,
  Provider,
  ProviderID,
  SecretInput,
} from "./api";
import {
  Brand,
  CopyValue,
  ErrorNotice,
  Icon,
  Modal,
  ProviderMark,
  providerNames,
} from "./ui";

export type Credential = SecretInput & {
  app: Application;
  secret: string;
  id: string;
  replacedSecretID?: string;
};
export class ConfirmationFailure extends Error {
  constructor(
    message: string,
    public retryable: boolean,
    cause?: unknown,
  ) {
    super(message, { cause });
    this.name = "ConfirmationFailure";
  }
}
export type Confirmation = {
  title: string;
  description: string;
  action: string;
  perform: () => Promise<void>;
};
const describeError = (error: unknown) =>
  error instanceof Error ? error.message : "An unexpected error occurred.";
export const Login = ({
  providers,
  loading,
  providerError,
  onRetry,
  onLogin,
  error,
  notice,
  blocked,
  preview,
}: {
  providers: Provider[];
  loading: boolean;
  providerError?: string;
  onRetry: () => void;
  onLogin: (provider: ProviderID) => Promise<void>;
  error?: string;
  notice?: ReactNode;
  blocked: boolean;
  preview: boolean;
}) => {
  const [failure, setFailure] = useState<string>();
  const [pending, setPending] = useState<ProviderID>();
  const busy = useRef(false);
  useEffect(() => {
    setFailure(undefined);
  }, [error]);
  const submit = async (provider: ProviderID) => {
    if (blocked || busy.current) return;
    busy.current = true;
    setPending(provider);
    setFailure(undefined);
    try {
      await onLogin(provider);
    } catch (cause) {
      setFailure(describeError(cause));
    } finally {
      busy.current = false;
      setPending(undefined);
    }
  };
  const available = providers.filter((provider) => provider.available);
  return (
    <main className="login-page">
      <div className="login-brand">
        <Brand />
      </div>
      <div className="login-layout">
        <section className="login-intro">
          <span className="eyebrow">AUTHENTICATION, CONNECTED</span>
          <h1>
            Every product.
            <br />
            One trusted gateway.
          </h1>
          <p>
            A shared home for your applications and third-party sign-in. Your
            products keep control of their users.
          </p>
          <div className="login-providers">
            <ProviderMark provider="microsoft" />
            <ProviderMark provider="google" />
            <ProviderMark provider="github" />
            <span>Microsoft · Google · GitHub</span>
          </div>
        </section>
        <section className="login-card">
          <span className="login-icon">
            <Icon name="shield" size={26} />
          </span>
          <h2>Welcome to your Console</h2>
          <p>Sign in to create and manage your applications.</p>
          {notice}
          {error !== undefined && <ErrorNotice message={error} />}
          {failure !== undefined && <ErrorNotice message={failure} />}
          {blocked && (
            <p className="login-help" role="status">
              Finish the pending operation and save any issued secret before
              signing in again.
            </p>
          )}
          {loading ? (
            <p role="status">Loading sign-in methods…</p>
          ) : providerError !== undefined ? (
            <>
              <ErrorNotice message={providerError} />
              <button className="button secondary full-width" onClick={onRetry}>
                Retry sign-in methods
              </button>
            </>
          ) : available.length === 0 ? (
            <p className="login-help" role="status">
              No sign-in providers are configured. Contact the platform
              operator.
            </p>
          ) : (
            <div className="oauth-buttons">
              {available.map(({ id }) => (
                <button
                  key={id}
                  className="button secondary full-width oauth-button"
                  disabled={blocked || pending !== undefined}
                  onClick={() => void submit(id)}
                >
                  <ProviderMark provider={id} />
                  {pending === id
                    ? "Connecting…"
                    : `Continue with ${providerNames[id]}`}
                  <Icon name="arrow" size={16} />
                </button>
              ))}
            </div>
          )}
          <div className="login-help">
            {preview
              ? "Choose a provider to try the Console with the selected preview account."
              : "Your identity is provided by your chosen sign-in service. New accounts are created automatically."}
          </div>
        </section>
      </div>
      <footer>UniAuth · A gateway for identity</footer>
    </main>
  );
};

export const AppEditor = ({
  app,
  providers,
  onClose,
  onSave,
  onUnauthorized,
}: {
  app: Application | "new";
  providers: Provider[];
  onClose: () => void;
  onSave: (input: AppInput | CreateAppInput) => Promise<void>;
  onUnauthorized: (cause: unknown) => void;
}) => {
  const callbackHintID = useId();
  const [name, setName] = useState(app === "new" ? "" : app.name);
  const [callbacks, setCallbacks] = useState(
    app === "new" ? "" : app.redirect_uris.join("\n"),
  );
  const [enabled, setEnabled] = useState(app === "new" ? true : app.enabled);
  const [secret, setSecret] = useState<SecretInput>({
    name: "",
    description: "",
  });
  const [selected, setSelected] = useState<ProviderID[]>(
    app === "new"
      ? providers
          .filter((provider) => provider.available)
          .map((provider) => provider.id)
      : app.providers,
  );
  const [pending, setPending] = useState(false);
  const submitting = useRef(false);
  const [error, setError] = useState<string>();
  const toggle = (id: ProviderID) =>
    setSelected((current) =>
      current.includes(id)
        ? current.filter((provider) => provider !== id)
        : [...current, id],
    );
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (submitting.current) return;
    setError(undefined);
    const redirectURIs = callbacks
      .split("\n")
      .map((uri) => uri.trim())
      .filter((uri) => uri.length > 0);
    if (new TextEncoder().encode(name.trim()).length > 128) {
      setError("Application name must be at most 128 UTF-8 bytes.");
      return;
    }
    if (redirectURIs.length > 16) {
      setError("Register at most 16 callback URLs.");
      return;
    }
    if (selected.length === 0) {
      setError("Select at least one identity provider.");
      return;
    }
    if (redirectURIs.length === 0) {
      setError("Register at least one callback URL.");
      return;
    }
    if (new Set(redirectURIs).size !== redirectURIs.length) {
      setError("Each callback URL must be unique.");
      return;
    }
    if (app === "new") {
      const validation = secretInputSchema.safeParse(secret);
      if (!validation.success) {
        setError(validation.error.issues[0].message);
        return;
      }
    }
    submitting.current = true;
    setPending(true);
    try {
      const input: AppInput = {
        name: name.trim(),
        enabled,
        redirect_uris: redirectURIs,
        providers: selected,
      };
      await onSave(
        app === "new" ? { ...input, initial_secret: secret } : input,
      );
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401)
        onUnauthorized(cause);
      else setError(describeError(cause));
    } finally {
      submitting.current = false;
      setPending(false);
    }
  };
  return (
    <Modal
      title={app === "new" ? "Create application" : "Edit application"}
      onClose={onClose}
      locked={pending}
      wide
    >
      <form onSubmit={submit}>
        <div className="modal-body">
          <p className="modal-description">
            Configure how this product signs in through UniAuth.
          </p>
          {error !== undefined && <ErrorNotice message={error} />}
          <label className="field">
            Application name
            <input
              autoFocus
              required
              maxLength={128}
              value={name}
              placeholder="e.g. Workspace"
              onChange={(event) => setName(event.target.value)}
            />
          </label>
          <label className="field">
            Callback URLs
            <textarea
              aria-label="Callback URLs"
              aria-describedby={callbackHintID}
              required
              rows={3}
              value={callbacks}
              placeholder="https://product.example/auth/callback"
              onChange={(event) => setCallbacks(event.target.value)}
            />
            <span id={callbackHintID} className="hint">
              One URL per line. HTTPS is required; loopback HTTP depends on
              deployment settings. URLs must match exactly.
            </span>
          </label>
          <fieldset className="provider-picker">
            <legend>Identity providers</legend>
            {providers.map((provider) => (
              <label
                key={provider.id}
                className={
                  selected.includes(provider.id)
                    ? "provider-option selected"
                    : "provider-option"
                }
              >
                <input
                  type="checkbox"
                  checked={selected.includes(provider.id)}
                  onChange={() => toggle(provider.id)}
                />
                <ProviderMark provider={provider.id} />
                <span>
                  <strong>{providerNames[provider.id]}</strong>
                  <small>
                    {provider.id === "microsoft"
                      ? "Personal accounts only"
                      : provider.id === "google"
                        ? "Google accounts"
                        : "GitHub accounts"}
                  </small>
                </span>
                {!provider.available && (
                  <span className="badge muted">Not configured</span>
                )}
              </label>
            ))}
          </fieldset>
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(event) => setEnabled(event.target.checked)}
            />
            <span>
              <strong>Enable application</strong>
              <small>Allow new login requests and identity retrieval.</small>
            </span>
          </label>
          {app === "new" && (
            <section className="initial-secret">
              <h3>Initial secret</h3>
              <p className="hint">
                Give the first credential a name and a note about where it will
                be used.
              </p>
              <SecretFields value={secret} onChange={setSecret} />
            </section>
          )}
        </div>
        <div className="modal-actions">
          <button
            className="button secondary"
            type="button"
            disabled={pending}
            onClick={onClose}
          >
            Cancel
          </button>
          <button className="button" type="submit" disabled={pending}>
            {pending
              ? "Saving…"
              : app === "new"
                ? "Create application"
                : "Save changes"}
          </button>
        </div>
      </form>
    </Modal>
  );
};
const SecretFields = ({
  value,
  onChange,
  autoFocus = false,
}: {
  value: SecretInput;
  onChange: (value: SecretInput) => void;
  autoFocus?: boolean;
}) => (
  <>
    <label className="field">
      Secret name
      <input
        aria-label="Secret name"
        required
        autoFocus={autoFocus}
        value={value.name}
        placeholder="e.g. Production backend"
        onChange={(event) => onChange({ ...value, name: event.target.value })}
      />
      <span className="hint">
        1–128 UTF-8 bytes. No surrounding whitespace or line breaks.
      </span>
    </label>
    <label className="field">
      Description
      <textarea
        aria-label="Description"
        rows={4}
        value={value.description}
        placeholder="Where this credential is used, who owns it, or why it exists."
        onChange={(event) =>
          onChange({ ...value, description: event.target.value })
        }
      />
      <span className="hint">
        Optional. Up to 4096 UTF-8 bytes; line breaks and spaces are kept.
      </span>
    </label>
  </>
);
export const SecretEditor = ({
  value,
  creating,
  onClose,
  onSave,
  onUnauthorized,
}: {
  value: SecretInput;
  creating: boolean;
  onClose: () => void;
  onSave: (input: SecretInput) => Promise<void>;
  onUnauthorized: (cause: unknown) => void;
}) => {
  const [draft, setDraft] = useState<SecretInput>({
    name: value.name,
    description: value.description,
  });
  const [pending, setPending] = useState(false);
  const submitting = useRef(false);
  const [error, setError] = useState<string>();
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (submitting.current) return;
    const validation = secretInputSchema.safeParse(draft);
    if (!validation.success) {
      setError(validation.error.issues[0].message);
      return;
    }
    submitting.current = true;
    setPending(true);
    setError(undefined);
    try {
      await onSave(validation.data);
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401)
        onUnauthorized(cause);
      else setError(describeError(cause));
    } finally {
      submitting.current = false;
      setPending(false);
    }
  };
  return (
    <Modal
      title={creating ? "Create secret" : "Edit secret details"}
      onClose={onClose}
      locked={pending}
    >
      <form onSubmit={submit}>
        <div className="modal-body">
          <p className="modal-description">
            {creating
              ? "Create a credential for this product backend. Save its value when it is shown."
              : "Update this credential's name and description. Its value and status stay the same."}
          </p>
          {error !== undefined && <ErrorNotice message={error} />}
          <SecretFields value={draft} onChange={setDraft} autoFocus />
        </div>
        <div className="modal-actions">
          <button
            className="button secondary"
            type="button"
            disabled={pending}
            onClick={onClose}
          >
            Cancel
          </button>
          <button className="button" type="submit" disabled={pending}>
            {pending ? "Saving…" : creating ? "Create secret" : "Save details"}
          </button>
        </div>
      </form>
    </Modal>
  );
};
export const CredentialDialog = ({
  credential,
  onClose,
}: {
  credential: Credential;
  onClose: () => void;
}) => {
  const [saved, setSaved] = useState(false);
  return (
    <Modal title="Save your app secret" onClose={onClose} locked>
      <div className="modal-body">
        <div className="credential-banner">
          <Icon name="key" />
          <div>
            <strong>This secret is shown once</strong>
            <p>
              Store it securely on the product backend before closing this
              dialog.
            </p>
          </div>
        </div>
        <p className="modal-description">
          Credential for <strong>{credential.app.name}</strong>
        </p>
        {credential.replacedSecretID !== undefined && (
          <p className="modal-description">
            Secret <code>{credential.replacedSecretID}</code> has been revoked.
            Update your product with this new key.
          </p>
        )}
        <CopyValue label="App ID" value={credential.app.id} />
        <CopyValue label="App secret" value={credential.secret} />
        <CopyValue label="Secret ID" value={credential.id} />
        <div className="credential-details">
          <span className="field-label">Secret name</span>
          <strong>{credential.name}</strong>
          <span className="field-label">Description</span>
          <p className="secret-description">
            {credential.description === ""
              ? "No description"
              : credential.description}
          </p>
        </div>
        <p className="hint">
          Keep this secret out of frontend code and source control. It cannot be
          retrieved again.
        </p>
        <label className="checkbox-field">
          <input
            type="checkbox"
            checked={saved}
            onChange={(event) => setSaved(event.target.checked)}
          />
          <span>I have saved this secret securely.</span>
        </label>
      </div>
      <div className="modal-actions">
        <button className="button" disabled={!saved} onClick={onClose}>
          Done
        </button>
      </div>
    </Modal>
  );
};
export const ConfirmDialog = ({
  confirmation,
  onClose,
  onError,
}: {
  confirmation: Confirmation;
  onClose: () => void;
  onError: (cause: unknown) => void;
}) => {
  const [pending, setPending] = useState(false);
  const submitting = useRef(false);
  const [error, setError] = useState<string>();
  const [retryable, setRetryable] = useState(true);
  const perform = async () => {
    if (submitting.current) return;
    submitting.current = true;
    setPending(true);
    setError(undefined);
    try {
      await confirmation.perform();
      onClose();
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401) onError(cause);
      else {
        setError(describeError(cause));
        if (cause instanceof ConfirmationFailure) setRetryable(cause.retryable);
      }
    } finally {
      submitting.current = false;
      setPending(false);
    }
  };
  return (
    <Modal title={confirmation.title} onClose={onClose} locked={pending}>
      <div className="modal-body">
        <p className="modal-description">{confirmation.description}</p>
        {error !== undefined && <ErrorNotice message={error} />}
      </div>
      <div className="modal-actions">
        <button
          className="button secondary"
          disabled={pending}
          onClick={onClose}
        >
          {retryable ? "Cancel" : "Close"}
        </button>
        <button
          className="button danger"
          disabled={pending || !retryable}
          onClick={perform}
        >
          {pending ? "Working…" : confirmation.action}
        </button>
      </div>
    </Modal>
  );
};
