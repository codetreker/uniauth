import { useEffect, useState } from "react";
import { APIError } from "./api";
import { AppEditor, ConfirmDialog, CredentialDialog, Login } from "./dialogs";
import type { Confirmation, Credential } from "./dialogs";

const describeError = (error: unknown) =>
  error instanceof Error ? error.message : "An unexpected error occurred.";
import type {
  Application,
  AppInput,
  ConsoleAPI,
  Provider,
  Secret,
} from "./api";
import {
  Brand,
  CopyValue,
  ErrorNotice,
  Icon,
  Loading,
  ProviderMark,
  providerNames,
} from "./ui";

const date = (value: string) =>
  new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    year: "numeric",
  }).format(new Date(value));
const appInput = (app: Application): AppInput => ({
  name: app.name,
  enabled: app.enabled,
  providers: app.providers,
  redirect_uris: app.redirect_uris,
});

export const Console = ({
  api,
  preview = false,
}: {
  api: ConsoleAPI;
  preview?: boolean;
}) => {
  const [session, setSession] = useState<
    "loading" | "signed-out" | "signed-in"
  >("loading");
  const [apps, setApps] = useState<Application[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [selected, setSelected] = useState<string>();
  const [section, setSection] = useState<"apps" | "providers">("apps");
  const [query, setQuery] = useState("");
  const [editor, setEditor] = useState<Application | "new">();
  const [credential, setCredential] = useState<Credential>();
  const [confirmation, setConfirmation] = useState<Confirmation>();
  const [pending, setPending] = useState(false);
  const [secrets, setSecrets] = useState<Secret[]>([]);
  const [secretsLoading, setSecretsLoading] = useState(false);
  const [secretsError, setSecretsError] = useState<string>();
  const [secretRevision, setSecretRevision] = useState(0);
  const current = apps.find((app) => app.id === selected);

  const reportError = (cause: unknown) => {
    setError(describeError(cause));
    if (cause instanceof APIError && cause.status === 401) {
      setSession("signed-out");
      setEditor(undefined);
      setConfirmation(undefined);
    }
  };
  const refresh = async () => {
    setLoading(true);
    setError(undefined);
    try {
      const [nextApps, nextProviders] = await Promise.all([
        api.listApps(),
        api.listProviders(),
      ]);
      setApps(nextApps);
      setProviders(nextProviders);
      setLoaded(true);
      setSelected((id) =>
        nextApps.some((app) => app.id === id) ? id : undefined,
      );
    } catch (cause) {
      reportError(cause);
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    let active = true;
    api
      .session()
      .then(() => {
        if (active) setSession("signed-in");
      })
      .catch((cause: unknown) => {
        if (!active) return;
        if (!(cause instanceof APIError && cause.status === 401))
          setError(describeError(cause));
        setSession("signed-out");
      });
    return () => {
      active = false;
    };
  }, [api]);
  useEffect(() => {
    if (session === "signed-in") void refresh();
  }, [session, api]);
  useEffect(() => {
    if (selected === undefined || session !== "signed-in") return;
    let active = true;
    setSecrets([]);
    setSecretsError(undefined);
    setSecretsLoading(true);
    api
      .listSecrets(selected)
      .then((result) => {
        if (active) setSecrets(result);
      })
      .catch((cause: unknown) => {
        if (active) {
          setSecretsError(describeError(cause));
          if (cause instanceof APIError && cause.status === 401)
            setSession("signed-out");
        }
      })
      .finally(() => {
        if (active) setSecretsLoading(false);
      });
    return () => {
      active = false;
    };
  }, [selected, session, secretRevision, api]);

  const saveApp = async (input: AppInput) => {
    if (editor === undefined)
      throw new Error("No application is being edited.");
    if (editor === "new") {
      const created = await api.createApp(input);
      setApps((existing) => [...existing, created.app]);
      setSelected(created.app.id);
      setCredential({
        app: created.app,
        secret: created.secret,
        id: created.secret_id,
      });
    } else {
      const updated = await api.updateApp(editor.id, input);
      setApps((existing) =>
        existing.map((app) => (app.id === updated.id ? updated : app)),
      );
    }
    setEditor(undefined);
    setError(undefined);
  };
  const toggleApp = async (app: Application) => {
    setPending(true);
    try {
      const updated = await api.updateApp(app.id, {
        ...appInput(app),
        enabled: !app.enabled,
      });
      setApps((existing) =>
        existing.map((item) => (item.id === app.id ? updated : item)),
      );
      setError(undefined);
    } catch (cause) {
      reportError(cause);
    } finally {
      setPending(false);
    }
  };
  const issueSecret = async (app: Application) => {
    setPending(true);
    try {
      const issued = await api.createSecret(app.id);
      setCredential({ app, secret: issued.secret, id: issued.id });
      setSecretRevision((value) => value + 1);
      setError(undefined);
    } catch (cause) {
      reportError(cause);
    } finally {
      setPending(false);
    }
  };
  const deleteApp = (app: Application) =>
    setConfirmation({
      title: "Delete application?",
      description: `This permanently deletes ${app.name} and its credentials. The product will no longer be able to start a login or retrieve an identity result.`,
      action: "Delete application",
      perform: async () => {
        await api.deleteApp(app.id);
        setApps((existing) => existing.filter((item) => item.id !== app.id));
        setSelected(undefined);
      },
    });
  const revokeSecret = (app: Application, secret: Secret) =>
    setConfirmation({
      title: "Revoke this secret?",
      description:
        "Requests using this secret will be rejected immediately. Make sure the product has switched to another active secret before revoking it.",
      action: "Revoke secret",
      perform: async () => {
        await api.revokeSecret(app.id, secret.id);
        setSecretRevision((value) => value + 1);
      },
    });
  const signOut = async () => {
    setPending(true);
    try {
      await api.logout();
      setSession("signed-out");
      setApps([]);
      setProviders([]);
      setSelected(undefined);
      setLoaded(false);
      setSecrets([]);
      setError(undefined);
    } catch (cause) {
      reportError(cause);
    } finally {
      setPending(false);
    }
  };
  const filtered = apps.filter((app) =>
    `${app.name} ${app.id}`.toLowerCase().includes(query.toLowerCase()),
  );

  return (
    <>
      {session === "loading" ? (
        <main className="boot">
          <Brand />
          <Loading label="Checking administrator session…" />
        </main>
      ) : session === "signed-out" ? (
        <Login
          api={api}
          error={error}
          preview={preview}
          onLogin={() => {
            setError(undefined);
            setSession("signed-in");
          }}
        />
      ) : (
        <div className="shell">
          <aside className="sidebar">
            <Brand />
            <div className="sidebar-label">WORKSPACE</div>
            <nav aria-label="Main navigation">
              <button
                className={section === "apps" ? "nav-item active" : "nav-item"}
                onClick={() => {
                  setSection("apps");
                  setSelected(undefined);
                }}
              >
                <Icon name="apps" />
                Applications<span className="nav-count">{apps.length}</span>
              </button>
              <button
                className={
                  section === "providers" ? "nav-item active" : "nav-item"
                }
                onClick={() => {
                  setSection("providers");
                  setSelected(undefined);
                }}
              >
                <Icon name="shield" />
                Identity providers
              </button>
            </nav>
            <div className="sidebar-bottom">
              <div className="system-indicator">
                <span />
                Authentication gateway
              </div>
              <div className="admin-row">
                <span className="avatar small">A</span>
                <div>
                  <strong>Administrator</strong>
                  <span>Console access</span>
                </div>
                <button
                  className="icon-button"
                  aria-label="Sign out"
                  disabled={pending}
                  onClick={signOut}
                >
                  <Icon name="logout" />
                </button>
              </div>
            </div>
          </aside>
          <div className="workspace">
            <header className="topbar">
              <div>
                <span className="breadcrumb">Workspace</span>
                <Icon name="arrow" size={14} />
                <span>
                  {section === "apps" ? "Applications" : "Identity providers"}
                </span>
                {current !== undefined && (
                  <>
                    <Icon name="arrow" size={14} />
                    <span>{current.name}</span>
                  </>
                )}
              </div>
              <span className={`environment ${preview ? "preview" : ""}`}>
                <span />
                {preview ? "Interactive preview" : "Admin console"}
              </span>
            </header>
            <main className="main-content">
              {preview && (
                <div className="preview-notice">
                  Interactive preview · Changes stay in this browser tab.{" "}
                  <button
                    onClick={() => {
                      void signOut();
                    }}
                  >
                    Try the login screen
                  </button>
                </div>
              )}
              {error !== undefined && <ErrorNotice message={error} />}
              {!loaded ? (
                loading ? (
                  <Loading label="Loading workspace…" />
                ) : (
                  <div className="empty-panel">
                    <h2>Workspace could not be loaded</h2>
                    <p>Check the connection and try again.</p>
                    <button
                      className="button"
                      onClick={() => {
                        void refresh();
                      }}
                    >
                      Try again
                    </button>
                  </div>
                )
              ) : section === "providers" ? (
                <>
                  <PageHeading
                    eyebrow="CONNECTIONS"
                    title="Identity providers"
                    description="Provider credentials are managed through deployment configuration."
                  />
                  <div className="provider-grid">
                    {providers.map((provider) => (
                      <article className="provider-card" key={provider.id}>
                        <ProviderMark provider={provider.id} />
                        <h2>{providerNames[provider.id]}</h2>
                        <p>
                          {provider.id === "microsoft"
                            ? "Personal Microsoft accounts"
                            : provider.id === "google"
                              ? "Google accounts"
                              : "GitHub accounts"}
                        </p>
                        <span
                          className={`badge ${provider.available ? "success" : "muted"}`}
                        >
                          {provider.available ? "Configured" : "Not configured"}
                        </span>
                      </article>
                    ))}
                  </div>
                  <div className="info-strip">
                    <Icon name="shield" />
                    <div>
                      <strong>One consistent login flow</strong>
                      <p>
                        Applications choose which providers to offer. Configure
                        upstream credentials in the service deployment to make a
                        provider available.
                      </p>
                    </div>
                  </div>
                </>
              ) : current !== undefined ? (
                <>
                  <button
                    className="back-link"
                    onClick={() => setSelected(undefined)}
                  >
                    ← All applications
                  </button>
                  <PageHeading
                    eyebrow="APPLICATION"
                    title={current.name}
                    description="Manage login configuration and product credentials."
                    actions={
                      <>
                        <button
                          className="button secondary"
                          disabled={pending}
                          onClick={() => {
                            void toggleApp(current);
                          }}
                        >
                          {current.enabled ? "Disable app" : "Enable app"}
                        </button>
                        <button
                          className="button"
                          onClick={() => setEditor(current)}
                        >
                          Edit application
                        </button>
                      </>
                    }
                  />
                  <div className="detail-grid">
                    <section className="panel configuration">
                      <div className="panel-heading">
                        <h2>Configuration</h2>
                        <span
                          className={`badge ${current.enabled ? "success" : "muted"}`}
                        >
                          {current.enabled ? "Enabled" : "Disabled"}
                        </span>
                      </div>
                      <CopyValue label="App ID" value={current.id} />
                      <div className="detail-section">
                        <h3>Identity providers</h3>
                        <div className="provider-pills">
                          {current.providers.map((id) => (
                            <span key={id}>
                              <ProviderMark provider={id} />
                              {providerNames[id]}
                              {!providers.some(
                                (provider) =>
                                  provider.id === id && provider.available,
                              ) && (
                                <span className="unavailable">
                                  Not configured
                                </span>
                              )}
                            </span>
                          ))}
                        </div>
                      </div>
                      <div className="detail-section">
                        <h3>Callback URLs</h3>
                        <div className="url-list">
                          {current.redirect_uris.map((uri) => (
                            <code key={uri}>{uri}</code>
                          ))}
                        </div>
                        <p className="hint">
                          Login requests must use an exactly registered callback
                          URL.
                        </p>
                      </div>
                      <div className="detail-meta">
                        <span>Created {date(current.created_at)}</span>
                        <span>Updated {date(current.updated_at)}</span>
                      </div>
                    </section>
                    <aside className="panel integration">
                      <span className="integration-icon">
                        <Icon name="key" size={25} />
                      </span>
                      <h2>Connect your product</h2>
                      <p>
                        Use the App ID and an active secret on your product
                        backend to retrieve one-time identity results.
                      </p>
                      <div className="integration-step">
                        <span>1</span>
                        <div>
                          <strong>Start a login</strong>
                          <code>GET /auth/authorize</code>
                        </div>
                      </div>
                      <div className="integration-step">
                        <span>2</span>
                        <div>
                          <strong>Handle the callback</strong>
                          <p>Verify your state and read the result code.</p>
                        </div>
                      </div>
                      <div className="integration-step">
                        <span>3</span>
                        <div>
                          <strong>Retrieve identity</strong>
                          <code>POST /auth/result</code>
                        </div>
                      </div>
                      <div className="note-small">
                        Results expire after 5 minutes and can be retrieved
                        once.
                      </div>
                    </aside>
                  </div>
                  <section className="panel secrets-panel">
                    <div className="panel-heading">
                      <div>
                        <h2>App secrets</h2>
                        <p>
                          Issue a replacement, update your product, then revoke
                          the previous secret.
                        </p>
                      </div>
                      <button
                        className="button secondary"
                        disabled={pending}
                        onClick={() => {
                          void issueSecret(current);
                        }}
                      >
                        <Icon name="plus" size={16} />
                        Create secret
                      </button>
                    </div>
                    {secretsLoading ? (
                      <Loading label="Loading credentials…" />
                    ) : secretsError !== undefined ? (
                      <>
                        <ErrorNotice message={secretsError} />
                        <button
                          className="button secondary"
                          onClick={() =>
                            setSecretRevision((value) => value + 1)
                          }
                        >
                          Try again
                        </button>
                      </>
                    ) : secrets.length === 0 ? (
                      <div className="compact-empty">
                        <Icon name="key" />
                        <p>
                          No secrets yet. Create a secret to connect this
                          application.
                        </p>
                      </div>
                    ) : (
                      <div className="table-scroll">
                        <table>
                          <thead>
                            <tr>
                              <th>Secret ID</th>
                              <th>Created</th>
                              <th>Status</th>
                              <th>
                                <span className="sr-only">Actions</span>
                              </th>
                            </tr>
                          </thead>
                          <tbody>
                            {secrets.map((secret) => (
                              <tr key={secret.id}>
                                <td>
                                  <code>{secret.id}</code>
                                </td>
                                <td>{date(secret.created_at)}</td>
                                <td>
                                  <span
                                    className={`badge ${secret.revoked_at === undefined ? "success" : "muted"}`}
                                  >
                                    {secret.revoked_at === undefined
                                      ? "Active"
                                      : "Revoked"}
                                  </span>
                                  {secret.revoked_at !== undefined && (
                                    <span className="revoked-date">
                                      {date(secret.revoked_at)}
                                    </span>
                                  )}
                                </td>
                                <td>
                                  {secret.revoked_at === undefined && (
                                    <button
                                      aria-label={`Revoke secret ${secret.id}`}
                                      className="text-button danger-text"
                                      disabled={pending}
                                      onClick={() =>
                                        revokeSecret(current, secret)
                                      }
                                    >
                                      Revoke
                                      <span className="sr-only">
                                        {" "}
                                        secret {secret.id}
                                      </span>
                                    </button>
                                  )}
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    )}
                  </section>
                  <div className="danger-zone">
                    <div>
                      <strong>Delete application</strong>
                      <p>
                        Permanently remove this application and all its
                        credentials.
                      </p>
                    </div>
                    <button
                      className="button danger-outline"
                      disabled={pending}
                      onClick={() => deleteApp(current)}
                    >
                      <Icon name="trash" size={16} />
                      Delete application
                    </button>
                  </div>
                </>
              ) : (
                <>
                  <PageHeading
                    eyebrow="YOUR PRODUCTS"
                    title="Applications"
                    description="One place to manage authentication for every product."
                    actions={
                      <button
                        className="button"
                        onClick={() => setEditor("new")}
                      >
                        <Icon name="plus" size={18} />
                        New application
                      </button>
                    }
                  />
                  <div className="stat-grid">
                    <Stat
                      label="Applications"
                      value={apps.length}
                      icon="apps"
                    />
                    <Stat
                      label="Enabled applications"
                      value={apps.filter((app) => app.enabled).length}
                      icon="check"
                    />
                    <Stat
                      label="Configured providers"
                      value={
                        providers.filter((provider) => provider.available)
                          .length
                      }
                      icon="shield"
                    />
                  </div>
                  <section className="panel apps-panel">
                    <div className="panel-heading">
                      <div>
                        <h2>
                          Registered applications
                          <span className="count">{apps.length}</span>
                        </h2>
                        <p>Manage where your users sign in.</p>
                      </div>
                      <label className="search">
                        <Icon name="search" size={18} />
                        <input
                          aria-label="Search applications"
                          placeholder="Search applications…"
                          value={query}
                          onChange={(event) => setQuery(event.target.value)}
                        />
                      </label>
                    </div>
                    {apps.length === 0 ? (
                      <div className="empty-panel">
                        <span className="empty-icon">
                          <Icon name="apps" size={32} />
                        </span>
                        <h2>Your products belong here</h2>
                        <p>
                          Create an application to get its App ID and secret,
                          <br />
                          then connect your product's login flow.
                        </p>
                        <button
                          className="button"
                          onClick={() => setEditor("new")}
                        >
                          <Icon name="plus" size={17} />
                          Create application
                        </button>
                      </div>
                    ) : filtered.length === 0 ? (
                      <div className="empty-panel">
                        <h2>No matching applications</h2>
                        <p>Try another name or App ID.</p>
                        <button
                          className="button secondary"
                          onClick={() => setQuery("")}
                        >
                          Clear search
                        </button>
                      </div>
                    ) : (
                      <div className="table-scroll">
                        <table className="apps-table">
                          <thead>
                            <tr>
                              <th>Application</th>
                              <th>Providers</th>
                              <th>Status</th>
                              <th>Updated</th>
                              <th>
                                <span className="sr-only">
                                  Open application
                                </span>
                              </th>
                            </tr>
                          </thead>
                          <tbody>
                            {filtered.map((app) => (
                              <tr key={app.id}>
                                <td>
                                  <button
                                    className="app-name"
                                    onClick={() => setSelected(app.id)}
                                  >
                                    <span
                                      className={`app-avatar color-${app.name.length % 4}`}
                                    >
                                      {app.name.substring(0, 1).toUpperCase()}
                                    </span>
                                    <span>
                                      <strong>{app.name}</strong>
                                      <code>{app.id}</code>
                                    </span>
                                  </button>
                                </td>
                                <td>
                                  <div className="provider-stack">
                                    {app.providers.map((id) => (
                                      <span key={id} title={providerNames[id]}>
                                        <ProviderMark provider={id} />
                                        <span className="sr-only">
                                          {providerNames[id]}
                                        </span>
                                      </span>
                                    ))}
                                  </div>
                                </td>
                                <td>
                                  <span
                                    className={`badge ${app.enabled ? "success" : "muted"}`}
                                  >
                                    <i />
                                    {app.enabled ? "Enabled" : "Disabled"}
                                  </span>
                                </td>
                                <td className="date-cell">
                                  {date(app.updated_at)}
                                </td>
                                <td>
                                  <button
                                    className="icon-button"
                                    aria-label={`Manage ${app.name}`}
                                    onClick={() => setSelected(app.id)}
                                  >
                                    <Icon name="arrow" size={18} />
                                  </button>
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    )}
                  </section>
                  <div className="footer-note">
                    <Icon name="shield" size={16} />
                    <span>
                      Products own their users and sessions. UniAuth securely
                      hands over third-party identity.
                    </span>
                  </div>
                </>
              )}
            </main>
            <footer className="workspace-footer">
              <span>UniAuth · Authentication gateway</span>
              <span>No user directory. No central SSO.</span>
            </footer>
          </div>
        </div>
      )}
      {editor !== undefined && session === "signed-in" && (
        <AppEditor
          app={editor}
          providers={providers}
          onClose={() => setEditor(undefined)}
          onSave={saveApp}
          onUnauthorized={reportError}
        />
      )}
      {credential !== undefined && (
        <CredentialDialog
          credential={credential}
          onClose={() => setCredential(undefined)}
        />
      )}
      {confirmation !== undefined && session === "signed-in" && (
        <ConfirmDialog
          confirmation={confirmation}
          onClose={() => setConfirmation(undefined)}
          onError={reportError}
        />
      )}
    </>
  );
};

const PageHeading = ({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow: string;
  title: string;
  description: string;
  actions?: React.ReactNode;
}) => (
  <div className="page-heading">
    <div>
      <span className="eyebrow">{eyebrow}</span>
      <h1>{title}</h1>
      <p>{description}</p>
    </div>
    {actions !== undefined && <div className="page-actions">{actions}</div>}
  </div>
);
const Stat = ({
  label,
  value,
  icon,
}: {
  label: string;
  value: number;
  icon: "apps" | "check" | "shield";
}) => (
  <div className="stat">
    <div>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
    <span className="stat-icon">
      <Icon name={icon} />
    </span>
  </div>
);
