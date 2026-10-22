import { useEffect, useId, useRef, useState } from "react";
import { APIError } from "./api";
import { AccountAvatar, AccountView, UsersView, accountName } from "./accounts";
import {
  AppEditor,
  ConfirmDialog,
  ConfirmationFailure,
  CredentialDialog,
  Login,
  SecretEditor,
} from "./dialogs";
import type { Confirmation, Credential } from "./dialogs";

import type {
  Account,
  Session,
  ProviderID,
  Application,
  AppInput,
  CreateAppInput,
  SecretInput,
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
import type { OAuthOutcome } from "./ui";

const loginErrors: Record<string, string> = {
  invalid_callback:
    "This sign-in request is expired, already used, or no longer valid. Start a new sign-in.",
  login_unavailable:
    "This sign-in request is expired, already used, or no longer valid. Start a new sign-in.",
  provider_failed:
    "The provider canceled or could not complete sign-in. Please try again.",
  login_failed: "Sign-in could not be completed. Please try again.",
  identity_conflict:
    "This identity belongs to another account and cannot be linked.",
  session_capacity:
    "Sign-in is temporarily unavailable. Please try again later.",
};
const describeError = (error: unknown) =>
  error instanceof Error ? error.message : "An unexpected error occurred.";

type AppPhase =
  | "editing"
  | "confirming"
  | "updating"
  | "saving"
  | "deleting"
  | "issuing"
  | "resetting-secret"
  | "revoking"
  | "credential";
type SecretTab = "active" | "disabled";
const secretTabs: { id: SecretTab; label: string }[] = [
  { id: "active", label: "Active" },
  { id: "disabled", label: "Disabled" },
];
type SecretDraft = { app: Application; secret: Secret | "new" };
type AppOwner = { key: string | symbol; token: symbol };
type OAuthOwner = {
  phase: "requesting" | "redirecting";
  cleanup?: () => void;
};
const newApplication = Symbol("new application");

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
  onAuthorization,
}: {
  api: ConsoleAPI;
  preview?: boolean;
  // A session completes authorization in-page; undefined means navigation has started.
  onAuthorization?: (url: string) => Promise<Session | undefined>;
}) => {
  const [session, setSession] = useState<
    "loading" | "signed-out" | "signed-in"
  >("loading");
  const [user, setUser] = useState<Account>();
  const [providersLoading, setProvidersLoading] = useState(true);
  const [providersError, setProvidersError] = useState<string>();
  const [providerRevision, setProviderRevision] = useState(0);
  const oauthPending = useRef<OAuthOwner>(undefined);
  const [oauthProvider, setOauthProvider] = useState<ProviderID>();
  const [oauthRedirecting, setOauthRedirecting] = useState(false);
  const oauthLoading = oauthProvider !== undefined;
  const [apps, setApps] = useState<Application[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [callbackError, setCallbackError] = useState<string>();
  const [selected, setSelected] = useState<string>();
  const [section, setSection] = useState<
    "apps" | "providers" | "account" | "users"
  >("apps");
  const [query, setQuery] = useState("");
  const [editor, setEditor] = useState<Application | "new">();
  const [secretEditor, setSecretEditor] = useState<SecretDraft>();
  const [credential, setCredential] = useState<Credential>();
  const [confirmation, setConfirmation] = useState<Confirmation>();
  const [pending, setPending] = useState(false);
  const [secrets, setSecrets] = useState<Secret[]>([]);
  const [secretsLoading, setSecretsLoading] = useState(false);
  const [secretsLoaded, setSecretsLoaded] = useState(false);
  const [secretView, setSecretView] = useState<{
    appID: string;
    tab: SecretTab;
  }>();
  const secretTabsID = useId();
  const secretTabButtons = useRef(new Map<SecretTab, HTMLButtonElement>());
  const [secretsError, setSecretsError] = useState<string>();
  const [secretRevision, setSecretRevision] = useState(0);
  const owners = useRef(
    new Map<string | symbol, { token: symbol; phase: AppPhase }>(),
  );
  const [operations, setOperations] = useState(
    new Map<string | symbol, AppPhase>(),
  );
  const snapshots = useRef<Application[]>([]);
  const unsettled = useRef(new Set<string>());
  const [unavailable, setUnavailable] = useState(new Set<string>());
  const editorOwner = useRef<AppOwner | undefined>(undefined);
  const secretEditorOwner = useRef<AppOwner | undefined>(undefined);
  const credentialOwner = useRef<AppOwner | undefined>(undefined);
  const secretConfigurationRevision = useRef(0);
  const secretReadGeneration = useRef(0);
  const secretAppID = useRef<string | undefined>(undefined);
  const confirmationOwner = useRef<AppOwner | undefined>(undefined);
  const logoutPending = useRef(false);
  const configurationRevision = useRef(0);
  const readGeneration = useRef(0);
  const current = apps.find((app) => app.id === selected);
  const secretTab =
    secretView !== undefined && secretView.appID === selected
      ? secretView.tab
      : "active";
  const currentSecrets = secretAppID.current === selected ? secrets : [];
  const activeSecrets = currentSecrets.filter(
    (secret) => secret.revoked_at === undefined,
  );
  const disabledSecrets = currentSecrets.filter(
    (secret) => secret.revoked_at !== undefined,
  );
  const visibleSecrets =
    secretTab === "active" ? activeSecrets : disabledSecrets;
  const secretCountsAvailable =
    secretAppID.current === selected &&
    secretsLoaded &&
    secretsError === undefined;

  const publishOperations = () =>
    setOperations(
      new Map([...owners.current].map(([key, owner]) => [key, owner.phase])),
    );
  // A synchronous owner also guards events dispatched before disabled controls render.
  const acquire = (
    key: string | symbol,
    phase: AppPhase,
  ): AppOwner | undefined => {
    if (
      owners.current.has(key) ||
      logoutPending.current ||
      oauthPending.current
    )
      return undefined;
    const token = Symbol("app operation");
    owners.current.set(key, { token, phase });
    configurationRevision.current += 1;
    publishOperations();
    return { key, token };
  };
  const transition = (owner: AppOwner, phase: AppPhase) => {
    if (owners.current.get(owner.key)?.token !== owner.token)
      throw new Error("Application operation ownership was lost.");
    owners.current.set(owner.key, { token: owner.token, phase });
    publishOperations();
  };
  const release = (owner: AppOwner) => {
    if (owners.current.get(owner.key)?.token !== owner.token) return;
    owners.current.delete(owner.key);
    publishOperations();
  };
  const replaceApps = (next: Application[]) => {
    snapshots.current = next;
    setApps(next);
  };
  const reconcile = (app: Application) =>
    replaceApps(
      snapshots.current.map((existing) =>
        existing.id === app.id ? app : existing,
      ),
    );
  const setUnsettled = (id: string, value: boolean) => {
    if (value) unsettled.current.add(id);
    else unsettled.current.delete(id);
    setUnavailable(new Set(unsettled.current));
  };
  const reloadApp = async (id: string) => {
    const latest = (await api.listApps()).find((app) => app.id === id);
    if (latest === undefined) {
      replaceApps(snapshots.current.filter((app) => app.id !== id));
      setSelected((selectedID) => (selectedID === id ? undefined : selectedID));
    } else {
      reconcile(latest);
    }
    setUnsettled(id, false);
  };
  const reconcileFailure = async (
    id: string,
    cause: unknown,
  ): Promise<unknown> => {
    if (cause instanceof APIError && cause.status === 401) return cause;
    try {
      await reloadApp(id);
      return cause;
    } catch (readError) {
      setUnsettled(id, true);
      if (readError instanceof APIError && readError.status === 401) {
        readError.cause = cause;
        return readError;
      }
      return new AggregateError(
        [cause, readError],
        `${describeError(cause)} Current application state could not be verified: ${describeError(readError)}`,
      );
    }
  };
  const refreshApp = async (app: Application) => {
    const owner = acquire(app.id, "updating");
    if (owner === undefined) return;
    try {
      await reloadApp(app.id);
      setError(undefined);
    } catch (cause) {
      setUnsettled(app.id, true);
      reportError(cause);
    } finally {
      release(owner);
    }
  };
  const closeEditor = () => {
    const owner = editorOwner.current;
    if (
      owner === undefined ||
      owners.current.get(owner.key)?.phase !== "editing"
    )
      return;
    release(owner);
    editorOwner.current = undefined;
    setEditor(undefined);
  };
  const dialogOwned = () =>
    oauthPending.current !== undefined ||
    editorOwner.current !== undefined ||
    secretEditorOwner.current !== undefined ||
    confirmationOwner.current !== undefined ||
    credentialOwner.current !== undefined ||
    [...owners.current.values()].some(
      ({ phase }) =>
        phase === "issuing" ||
        phase === "saving" ||
        phase === "deleting" ||
        phase === "revoking" ||
        phase === "resetting-secret",
    );
  const closeCredential = () => {
    const owner = credentialOwner.current;
    if (owner === undefined) return;
    release(owner);
    credentialOwner.current = undefined;
    setCredential(undefined);
  };
  const closeSecretEditor = () => {
    const owner = secretEditorOwner.current;
    if (
      owner === undefined ||
      owners.current.get(owner.key)?.phase !== "editing"
    )
      return;
    release(owner);
    secretEditorOwner.current = undefined;
    setSecretEditor(undefined);
  };
  const closeConfirmation = () => {
    const owner = confirmationOwner.current;
    if (owner !== undefined) {
      if (owners.current.get(owner.key)?.phase !== "confirming") return;
      release(owner);
      confirmationOwner.current = undefined;
    }
    setConfirmation(undefined);
  };
  const openEditor = (app: Application | "new") => {
    if (dialogOwned() || loading) return;
    const snapshot =
      app === "new"
        ? "new"
        : snapshots.current.find((item) => item.id === app.id);
    if (
      snapshot === undefined ||
      (snapshot !== "new" && unsettled.current.has(snapshot.id))
    )
      return;
    const owner = acquire(
      snapshot === "new" ? newApplication : snapshot.id,
      "editing",
    );
    if (owner === undefined) return;
    editorOwner.current = owner;
    setEditor(snapshot);
  };
  const navigate = (change: () => void) => {
    if (dialogOwned()) return;
    setSecretView(undefined);
    change();
  };
  const selectSecretTab = (tab: SecretTab) => {
    if (
      selected === undefined ||
      dialogOwned() ||
      owners.current.has(selected) ||
      logoutPending.current
    )
      return false;
    setSecretView({ appID: selected, tab });
    return true;
  };
  const appStatus = (app: Application) => {
    const phase = operations.get(app.id);
    if (unavailable.has(app.id)) return "State unavailable";
    return phase === "updating" || phase === "saving"
      ? "Updating…"
      : phase === "deleting"
        ? "Deleting…"
        : app.enabled
          ? "Enabled"
          : "Disabled";
  };
  const reportError = (cause: unknown) => {
    setError(describeError(cause));
    if (cause instanceof APIError && cause.status === 401) {
      setSession("signed-out");
      setUser(undefined);
      setLoaded(false);
      // Discard unauthorized drafts; dispatched writes retain ownership until settlement.
      for (const owner of [
        editorOwner.current,
        confirmationOwner.current,
        secretEditorOwner.current,
      ]) {
        if (owner === undefined) continue;
        const phase = owners.current.get(owner.key)?.phase;
        if (phase === "editing" || phase === "confirming") release(owner);
      }
      secretEditorOwner.current = undefined;
      setSecretEditor(undefined);
      editorOwner.current = undefined;
      confirmationOwner.current = undefined;
      setEditor(undefined);
      setConfirmation(undefined);
    }
  };
  const refresh = async () => {
    const generation = ++readGeneration.current;
    const revision = configurationRevision.current;
    setLoading(true);
    setError(undefined);
    try {
      const [nextApps, nextProviders] = await Promise.all([
        api.listApps(),
        api.listProviders(),
      ]);
      if (
        generation !== readGeneration.current ||
        revision !== configurationRevision.current
      )
        return;
      replaceApps(nextApps);
      unsettled.current.clear();
      setUnavailable(new Set());
      setProviders(nextProviders);
      setLoaded(true);
      setSelected((id) =>
        nextApps.some((app) => app.id === id) ? id : undefined,
      );
    } catch (cause) {
      reportError(cause);
    } finally {
      if (generation === readGeneration.current) setLoading(false);
    }
  };
  useEffect(() => {
    let active = true;
    api
      .session()
      .then((result) => {
        if (active) {
          setUser(result.user);
          setSession("signed-in");
        }
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
    let active = true;
    setProvidersLoading(true);
    setProvidersError(undefined);
    api
      .listProviders()
      .then((result) => {
        if (active) setProviders(result);
      })
      .catch((cause: unknown) => {
        if (active) setProvidersError(describeError(cause));
      })
      .finally(() => {
        if (active) setProvidersLoading(false);
      });
    return () => {
      active = false;
    };
  }, [api, providerRevision]);
  useEffect(() => {
    const parameters = new URLSearchParams(window.location.search);
    const failure = parameters.get("login_error");
    if (failure === null) return;
    setCallbackError(
      Object.hasOwn(loginErrors, failure)
        ? loginErrors[failure]
        : "Sign-in could not be completed. Please try again.",
    );
    parameters.delete("login_error");
    const query = parameters.toString();
    window.history.replaceState(
      null,
      "",
      `${window.location.pathname}${query === "" ? "" : `?${query}`}${window.location.hash}`,
    );
  }, []);
  useEffect(() => {
    if (session === "signed-in") void refresh();
  }, [session, api]);
  useEffect(() => {
    if (selected === undefined || session !== "signed-in") return;
    let active = true;
    if (secretAppID.current !== selected) {
      secretAppID.current = selected;
      setSecrets([]);
      setSecretsLoaded(false);
    }
    const revision = secretConfigurationRevision.current;
    const generation = ++secretReadGeneration.current;
    setSecretsError(undefined);
    setSecretsLoading(true);
    api
      .listSecrets(selected)
      .then((result) => {
        if (
          active &&
          revision === secretConfigurationRevision.current &&
          generation === secretReadGeneration.current
        ) {
          setSecrets(result);
          setSecretsLoaded(true);
        }
      })
      .catch((cause: unknown) => {
        if (
          active &&
          revision === secretConfigurationRevision.current &&
          generation === secretReadGeneration.current
        ) {
          setSecretsError(describeError(cause));
          if (cause instanceof APIError && cause.status === 401)
            reportError(cause);
        }
      })
      .finally(() => {
        if (active && generation === secretReadGeneration.current)
          setSecretsLoading(false);
      });
    return () => {
      active = false;
    };
  }, [selected, session, secretRevision, api]);

  const saveApp = async (input: CreateAppInput) => {
    const owner = editorOwner.current;
    if (editor === undefined || owner === undefined)
      throw new Error("No application is being edited.");
    if (owners.current.get(owner.key)?.phase !== "editing")
      throw new Error("Application changes are already being saved.");
    transition(owner, editor === "new" ? "issuing" : "saving");
    try {
      if (editor === "new") {
        const created = await api.createApp(input);
        replaceApps([...snapshots.current, created.app]);
        setSelected(created.app.id);
        if (created.secret !== undefined) {
          if (input.initial_secret === undefined)
            throw new Error(
              "Unexpected initial secret in application response.",
            );
          setCredential({
            app: created.app,
            secret: created.secret,
            id: created.secret_id,
            ...input.initial_secret,
          });
          transition(owner, "credential");
          credentialOwner.current = owner;
        } else {
          release(owner);
        }
      } else {
        const updated = await api.updateApp(editor.id, input);
        reconcile(updated);
      }
      if (editor !== "new") release(owner);
      editorOwner.current = undefined;
      setEditor(undefined);
      setError(undefined);
    } catch (cause) {
      const failure =
        editor === "new" ? cause : await reconcileFailure(editor.id, cause);
      if (editorOwner.current === owner) transition(owner, "editing");
      else release(owner);
      throw failure;
    }
  };
  const toggleApp = async (app: Application) => {
    if (dialogOwned() || unsettled.current.has(app.id)) return;
    const owner = acquire(app.id, "updating");
    if (owner === undefined) return;
    const snapshot = snapshots.current.find(
      (existing) => existing.id === app.id,
    );
    if (snapshot === undefined) {
      release(owner);
      return;
    }
    setPending(true);
    try {
      const updated = await api.updateApp(app.id, {
        ...appInput(snapshot),
        enabled: !snapshot.enabled,
      });
      reconcile(updated);
      setError(undefined);
    } catch (cause) {
      reportError(await reconcileFailure(app.id, cause));
    } finally {
      release(owner);
      setPending(false);
    }
  };
  const openSecretEditor = (app: Application, secret: Secret | "new") => {
    if (dialogOwned() || unsettled.current.has(app.id)) return;
    const owner = acquire(app.id, "editing");
    if (owner === undefined) return;
    secretEditorOwner.current = owner;
    setSecretEditor({ app, secret });
  };
  const saveSecret = async (input: SecretInput) => {
    const owner = secretEditorOwner.current;
    if (owner === undefined || secretEditor === undefined)
      throw new Error("No secret is being edited.");
    if (owners.current.get(owner.key)?.phase !== "editing")
      throw new Error("Secret changes are already being saved.");
    const { app, secret } = secretEditor;
    transition(owner, secret === "new" ? "issuing" : "saving");
    secretConfigurationRevision.current += 1;
    try {
      if (secret === "new") {
        const issued = await api.createSecret(app.id, input);
        setSecretView({ appID: app.id, tab: "active" });
        setCredential({
          app,
          secret: issued.secret,
          id: issued.id,
          name: issued.name,
          description: issued.description,
        });
        transition(owner, "credential");
        credentialOwner.current = owner;
        setSecrets((existing) => [
          ...existing,
          {
            id: issued.id,
            name: issued.name,
            description: issued.description,
            created_at: issued.created_at,
          },
        ]);
      } else {
        const updated = await api.updateSecret(app.id, secret.id, input);
        setSecrets((existing) =>
          existing.map((item) => (item.id === updated.id ? updated : item)),
        );
        release(owner);
      }
      secretEditorOwner.current = undefined;
      setSecretEditor(undefined);
      setSecretRevision((value) => value + 1);
      setError(undefined);
    } catch (cause) {
      const failure = await reconcileFailure(app.id, cause);
      if (secretEditorOwner.current === owner) transition(owner, "editing");
      else release(owner);
      setSecretRevision((value) => value + 1);
      throw failure;
    }
  };
  const deleteApp = (app: Application) => {
    if (unsettled.current.has(app.id)) return;
    if (dialogOwned()) return;
    const owner = acquire(app.id, "confirming");
    if (owner === undefined) return;
    confirmationOwner.current = owner;
    setConfirmation({
      title: "Delete application?",
      description: `This permanently deletes ${app.name} and its credentials. The product will no longer be able to start a login or retrieve an identity result.`,
      action: "Delete application",
      perform: async () => {
        if (owners.current.get(owner.key)?.phase !== "confirming")
          throw new Error("Application deletion is already in progress.");
        transition(owner, "deleting");
        try {
          await api.deleteApp(app.id);
          replaceApps(snapshots.current.filter((item) => item.id !== app.id));
          setSelected((id) => (id === app.id ? undefined : id));
          release(owner);
          confirmationOwner.current = undefined;
        } catch (cause) {
          const failure = await reconcileFailure(app.id, cause);
          if (confirmationOwner.current === owner)
            transition(owner, "confirming");
          else release(owner);
          throw failure;
        }
      },
    });
  };
  const revokeSecret = (app: Application, secret: Secret) => {
    if (dialogOwned() || unsettled.current.has(app.id)) return;
    const owner = acquire(app.id, "confirming");
    if (owner === undefined) return;
    confirmationOwner.current = owner;
    setConfirmation({
      title: "Revoke this secret?",
      description: `Revoke “${secret.name}” (${secret.id})? Requests using this secret will be rejected immediately. Make sure the product has switched to another active secret before revoking it.`,
      action: "Revoke secret",
      perform: async () => {
        if (owners.current.get(owner.key)?.phase !== "confirming")
          throw new Error("Secret revocation is already in progress.");
        transition(owner, "revoking");
        secretConfigurationRevision.current += 1;
        try {
          await api.revokeSecret(app.id, secret.id);
          setSecretRevision((value) => value + 1);
          release(owner);
          confirmationOwner.current = undefined;
        } catch (cause) {
          const failure = await reconcileFailure(app.id, cause);
          if (confirmationOwner.current === owner)
            transition(owner, "confirming");
          else release(owner);
          setSecretRevision((value) => value + 1);
          throw failure;
        }
      },
    });
  };
  const resetSecret = (app: Application, secret: Secret) => {
    if (
      dialogOwned() ||
      unsettled.current.has(app.id) ||
      secret.revoked_at !== undefined ||
      secretsLoading ||
      secretsError !== undefined
    )
      return;
    const owner = acquire(app.id, "confirming");
    if (owner === undefined) return;
    confirmationOwner.current = owner;
    const restoreConfirmation = () => {
      if (confirmationOwner.current === owner) transition(owner, "confirming");
      else release(owner);
    };
    setConfirmation({
      title: "Reset this secret?",
      description: `Reset “${secret.name}” (${secret.id}) for ${app.name}? The current key will stop working immediately. The new key will be shown once. If the reset response is lost, the new key cannot be recovered.`,
      action: "Reset secret",
      perform: async () => {
        if (owners.current.get(owner.key)?.phase !== "confirming")
          throw new Error("Secret reset is already in progress.");
        transition(owner, "resetting-secret");
        secretConfigurationRevision.current += 1;
        try {
          const issued = await api.resetSecret(app.id, secret.id);
          secretReadGeneration.current += 1;
          setSecretsLoading(true);
          setSecretRevision((value) => value + 1);
          setCredential({
            app,
            secret: issued.secret,
            id: issued.id,
            name: issued.name,
            description: issued.description,
            replacedSecretID: secret.id,
          });
          setError(undefined);
          transition(owner, "credential");
          credentialOwner.current = owner;
          confirmationOwner.current = undefined;
          setConfirmation(undefined);
        } catch (cause) {
          if (cause instanceof APIError && cause.status === 401) {
            restoreConfirmation();
            throw cause;
          }
          let currentSecrets: Secret[];
          try {
            currentSecrets = await api.listSecrets(app.id);
          } catch (readError) {
            restoreConfirmation();
            if (readError instanceof APIError && readError.status === 401) {
              readError.cause = cause;
              throw readError;
            }
            secretReadGeneration.current += 1;
            setSecretsError(describeError(readError));
            setSecretsLoading(false);
            throw new ConfirmationFailure(
              `The reset outcome could not be verified: ${describeError(readError)} Refresh the secret list before another reset. If secret ${secret.id} is revoked, create another secret.`,
              false,
              new AggregateError(
                [cause, readError],
                "Secret reset and reconciliation failed.",
              ),
            );
          }
          secretReadGeneration.current += 1;
          setSecrets(currentSecrets);
          setSecretsLoaded(true);
          setSecretsError(undefined);
          setSecretsLoading(false);
          restoreConfirmation();
          const currentSecret = currentSecrets.find(
            (item) => item.id === secret.id,
          );
          if (
            currentSecret === undefined ||
            currentSecret.revoked_at !== undefined ||
            (cause instanceof APIError && cause.code === "already_revoked")
          )
            throw new ConfirmationFailure(
              `Secret ${secret.id} is revoked, but the new key was not received. Create another secret and update your product.`,
              false,
              cause,
            );
          throw cause;
        }
      },
    });
  };
  const releaseOAuth = (owner: OAuthOwner) => {
    if (oauthPending.current !== owner) return;
    owner.cleanup?.();
    oauthPending.current = undefined;
    setOauthProvider(undefined);
    setOauthRedirecting(false);
  };
  const watchOAuthNavigation = (owner: OAuthOwner) => {
    const navigation = window.navigation;
    if (navigation === undefined) return;
    // Browser Stop aborts this signal without restoring the document through pageshow.
    // https://html.spec.whatwg.org/multipage/nav-history-apis.html#abort-a-navigateevent
    const started = (event: NavigateEvent) => {
      navigation.removeEventListener("navigate", started);
      if (oauthPending.current !== owner) return;
      owner.phase = "redirecting";
      setOauthRedirecting(true);
      const cancelled = () => releaseOAuth(owner);
      owner.cleanup = () =>
        event.signal.removeEventListener("abort", cancelled);
      event.signal.addEventListener("abort", cancelled, { once: true });
      if (event.signal.aborted) cancelled();
    };
    owner.cleanup = () => navigation.removeEventListener("navigate", started);
    navigation.addEventListener("navigate", started, { once: true });
  };
  const cancelOAuth = () => {
    const owner = oauthPending.current;
    if (owner?.phase !== "redirecting") return;
    // Stop the old load before another operation can acquire the Console.
    window.stop();
    releaseOAuth(owner);
  };
  useEffect(() => {
    const restore = (event: PageTransitionEvent) => {
      const owner = oauthPending.current;
      if (event.persisted && owner?.phase === "redirecting")
        releaseOAuth(owner);
    };
    window.addEventListener("pageshow", restore);
    return () => {
      window.removeEventListener("pageshow", restore);
      oauthPending.current?.cleanup?.();
      oauthPending.current = undefined;
    };
  }, []);
  const startOAuth = async (
    provider: ProviderID,
    link = false,
  ): Promise<OAuthOutcome> => {
    if (
      owners.current.size !== 0 ||
      logoutPending.current ||
      oauthPending.current
    )
      return "blocked";
    const owner: OAuthOwner = { phase: "requesting" };
    oauthPending.current = owner;
    setOauthProvider(provider);
    setCallbackError(undefined);
    try {
      const result = await (link
        ? api.linkIdentity(provider)
        : api.login(provider));
      if (oauthPending.current !== owner) return "cancelled";
      watchOAuthNavigation(owner);
      let nextSession: Session | undefined;
      if (onAuthorization === undefined) {
        owner.phase = "redirecting";
        setOauthRedirecting(true);
        window.location.assign(result.authorization_url);
      } else nextSession = await onAuthorization(result.authorization_url);
      if (oauthPending.current !== owner) return "cancelled";
      if (nextSession === undefined) {
        owner.phase = "redirecting";
        setOauthRedirecting(true);
        return "redirecting";
      }
      setUser(nextSession.user);
      setSession("signed-in");
      setError(undefined);
      if (!link) {
        setSection("apps");
        setSelected(undefined);
      }
      releaseOAuth(owner);
      return "completed";
    } catch (cause) {
      releaseOAuth(owner);
      throw cause;
    }
  };
  const signOut = async () => {
    if (
      owners.current.size !== 0 ||
      logoutPending.current ||
      oauthPending.current
    )
      return;
    logoutPending.current = true;
    setPending(true);
    try {
      await api.logout();
      setSession("signed-out");
      setCallbackError(undefined);
      replaceApps([]);
      setUser(undefined);
      setSection("apps");
      setSelected(undefined);
      setLoaded(false);
      setSecrets([]);
      setSecretsLoaded(false);
      setError(undefined);
    } catch (cause) {
      reportError(cause);
    } finally {
      logoutPending.current = false;
      setPending(false);
    }
  };
  const filtered = apps.filter((app) =>
    `${app.name} ${app.id}`.toLowerCase().includes(query.toLowerCase()),
  );

  const callbackNotice =
    callbackError === undefined ? undefined : (
      <div className="callback-notice">
        <ErrorNotice message={callbackError} />
        <button
          className="icon-button"
          aria-label="Dismiss sign-in notice"
          onClick={() => setCallbackError(undefined)}
        >
          <Icon name="close" size={16} />
        </button>
      </div>
    );
  return (
    <>
      {session === "loading" ? (
        <main className="boot">
          <Brand />
          <Loading label="Checking your session…" />
        </main>
      ) : session === "signed-out" ? (
        <Login
          providers={providers}
          loading={providersLoading}
          providerError={providersError}
          onRetry={() => setProviderRevision((value) => value + 1)}
          error={error}
          notice={callbackNotice}
          preview={preview}
          blocked={operations.size !== 0}
          connecting={oauthProvider}
          onLogin={(provider) => startOAuth(provider)}
        />
      ) : (
        <div className="shell">
          <aside className="sidebar">
            <Brand />
            <div className="sidebar-label">WORKSPACE</div>
            <nav aria-label="Main navigation" aria-busy={dialogOwned()}>
              <button
                disabled={dialogOwned()}
                className={section === "apps" ? "nav-item active" : "nav-item"}
                onClick={() => {
                  navigate(() => {
                    setSection("apps");
                    setSelected(undefined);
                  });
                }}
              >
                <Icon name="apps" />
                Applications<span className="nav-count">{apps.length}</span>
              </button>
              <button
                disabled={dialogOwned()}
                className={
                  section === "providers" ? "nav-item active" : "nav-item"
                }
                onClick={() => {
                  navigate(() => {
                    setSection("providers");
                    setSelected(undefined);
                  });
                }}
              >
                <Icon name="shield" />
                Identity providers
              </button>
              <button
                disabled={dialogOwned()}
                className={
                  section === "account" ? "nav-item active" : "nav-item"
                }
                onClick={() =>
                  navigate(() => {
                    setSection("account");
                    setSelected(undefined);
                  })
                }
              >
                <Icon name="key" />
                My account
              </button>
              {user?.is_admin === true && (
                <button
                  disabled={dialogOwned()}
                  className={
                    section === "users" ? "nav-item active" : "nav-item"
                  }
                  onClick={() =>
                    navigate(() => {
                      setSection("users");
                      setSelected(undefined);
                    })
                  }
                >
                  <Icon name="apps" />
                  Users
                </button>
              )}
            </nav>
            <div className="sidebar-bottom">
              <div className="system-indicator">
                <span />
                Authentication gateway
              </div>
              <div className="account-row">
                {user !== undefined && <AccountAvatar user={user} />}
                <div>
                  <strong>{user === undefined ? "" : accountName(user)}</strong>
                  <span>
                    {user?.is_admin === true
                      ? "Platform administrator"
                      : "Platform member"}
                  </span>
                </div>
                <button
                  className="icon-button"
                  aria-label="Sign out"
                  disabled={pending || operations.size !== 0 || oauthLoading}
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
                  {section === "apps"
                    ? "Applications"
                    : section === "providers"
                      ? "Identity providers"
                      : section === "account"
                        ? "My account"
                        : "Users"}
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
                {preview ? "Interactive preview" : "Your console"}
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
              {callbackNotice}
              {error !== undefined && <ErrorNotice message={error} />}
              {section === "account" && user !== undefined ? (
                <AccountView
                  api={api}
                  user={user}
                  providers={providers}
                  blocked={operations.size !== 0}
                  connecting={oauthProvider}
                  onLink={(provider) => startOAuth(provider, true)}
                  onUnauthorized={reportError}
                />
              ) : section === "users" && user?.is_admin === true ? (
                <UsersView api={api} onUnauthorized={reportError} />
              ) : !loaded ? (
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
                    disabled={dialogOwned()}
                    onClick={() => navigate(() => setSelected(undefined))}
                  >
                    ← All applications
                  </button>
                  <PageHeading
                    eyebrow="APPLICATION"
                    title={current.name}
                    description="Manage login configuration and product credentials."
                    actions={
                      <>
                        {unavailable.has(current.id) && (
                          <button
                            className="button secondary"
                            disabled={operations.has(current.id)}
                            onClick={() => {
                              void refreshApp(current);
                            }}
                          >
                            Refresh application
                          </button>
                        )}
                        <button
                          className="button secondary"
                          disabled={
                            pending ||
                            operations.has(current.id) ||
                            unavailable.has(current.id)
                          }
                          onClick={() => {
                            void toggleApp(current);
                          }}
                        >
                          {current.enabled ? "Disable app" : "Enable app"}
                        </button>
                        <button
                          className="button"
                          disabled={
                            operations.has(current.id) ||
                            pending ||
                            unavailable.has(current.id)
                          }
                          onClick={() => openEditor(current)}
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
                          className={`badge ${unavailable.has(current.id) || operations.get(current.id) === "updating" || operations.get(current.id) === "saving" || operations.get(current.id) === "deleting" ? "muted" : current.enabled ? "success" : "muted"}`}
                        >
                          {appStatus(current)}
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
                        disabled={
                          pending ||
                          operations.has(current.id) ||
                          unavailable.has(current.id)
                        }
                        onClick={() => {
                          openSecretEditor(current, "new");
                        }}
                      >
                        <Icon name="plus" size={16} />
                        Create secret
                      </button>
                    </div>
                    <div
                      className="secret-tabs"
                      role="tablist"
                      aria-label="App secrets"
                    >
                      {secretTabs.map(({ id, label }) => (
                        <button
                          key={id}
                          id={`${secretTabsID}-${id}-tab`}
                          ref={(button) => {
                            if (button === null)
                              secretTabButtons.current.delete(id);
                            else secretTabButtons.current.set(id, button);
                          }}
                          role="tab"
                          aria-selected={secretTab === id}
                          aria-controls={`${secretTabsID}-${id}-panel`}
                          tabIndex={secretTab === id ? 0 : -1}
                          disabled={dialogOwned() || operations.has(current.id)}
                          onClick={() => selectSecretTab(id)}
                          onKeyDown={(event) => {
                            const next =
                              event.key === "Home"
                                ? "active"
                                : event.key === "End"
                                  ? "disabled"
                                  : event.key === "ArrowLeft" ||
                                      event.key === "ArrowRight"
                                    ? id === "active"
                                      ? "disabled"
                                      : "active"
                                    : undefined;
                            if (next === undefined) return;
                            event.preventDefault();
                            if (selectSecretTab(next))
                              secretTabButtons.current.get(next)?.focus();
                          }}
                        >
                          {label}
                          {secretCountsAvailable && (
                            <>
                              {" "}
                              <span className="count">
                                {id === "active"
                                  ? activeSecrets.length
                                  : disabledSecrets.length}
                              </span>
                            </>
                          )}
                        </button>
                      ))}
                    </div>
                    {secretTabs.map(({ id }) => (
                      <div
                        key={id}
                        id={`${secretTabsID}-${id}-panel`}
                        role="tabpanel"
                        aria-labelledby={`${secretTabsID}-${id}-tab`}
                        hidden={secretTab !== id}
                        tabIndex={0}
                      >
                        {secretTab === id && (
                          <>
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
                            ) : visibleSecrets.length === 0 ? (
                              <div className="compact-empty">
                                <Icon name="key" />
                                <p>
                                  {secretTab === "active"
                                    ? "No active secrets. Create a secret to connect this application."
                                    : "No disabled secrets. Revoked secrets appear here."}
                                </p>
                              </div>
                            ) : (
                              <div className="table-scroll">
                                <table>
                                  <thead>
                                    <tr>
                                      <th>Secret</th>
                                      <th>Created</th>
                                      <th>Status</th>
                                      <th>
                                        <span className="sr-only">Actions</span>
                                      </th>
                                    </tr>
                                  </thead>
                                  <tbody>
                                    {visibleSecrets.map((secret) => (
                                      <tr key={secret.id}>
                                        <td>
                                          <div className="secret-details">
                                            <strong>{secret.name}</strong>
                                          </div>
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
                                          <div className="secret-actions">
                                            <button
                                              aria-label={`Edit details for secret ${secret.id}`}
                                              className="text-button"
                                              disabled={
                                                pending ||
                                                operations.has(current.id) ||
                                                unavailable.has(current.id)
                                              }
                                              onClick={() =>
                                                openSecretEditor(
                                                  current,
                                                  secret,
                                                )
                                              }
                                            >
                                              Edit details
                                            </button>
                                            {secret.revoked_at ===
                                              undefined && (
                                              <>
                                                <button
                                                  aria-label={`Reset secret ${secret.id}`}
                                                  className="text-button"
                                                  disabled={
                                                    pending ||
                                                    operations.has(
                                                      current.id,
                                                    ) ||
                                                    unavailable.has(
                                                      current.id,
                                                    ) ||
                                                    secretsLoading ||
                                                    secretsError !== undefined
                                                  }
                                                  onClick={() =>
                                                    resetSecret(current, secret)
                                                  }
                                                >
                                                  Reset
                                                </button>
                                                <button
                                                  aria-label={`Revoke secret ${secret.id}`}
                                                  className="text-button danger-text"
                                                  disabled={
                                                    pending ||
                                                    operations.has(
                                                      current.id,
                                                    ) ||
                                                    unavailable.has(current.id)
                                                  }
                                                  onClick={() =>
                                                    revokeSecret(
                                                      current,
                                                      secret,
                                                    )
                                                  }
                                                >
                                                  Revoke
                                                </button>
                                              </>
                                            )}
                                          </div>
                                        </td>
                                      </tr>
                                    ))}
                                  </tbody>
                                </table>
                              </div>
                            )}
                          </>
                        )}
                      </div>
                    ))}
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
                      disabled={
                        pending ||
                        operations.has(current.id) ||
                        unavailable.has(current.id)
                      }
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
                        onClick={() => openEditor("new")}
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
                          Create an application to get its App ID,
                          <br />
                          then connect your product's login flow.
                        </p>
                        <button
                          className="button"
                          onClick={() => openEditor("new")}
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
                                    onClick={() =>
                                      navigate(() => setSelected(app.id))
                                    }
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
                                    className={`badge ${unavailable.has(app.id) || operations.get(app.id) === "updating" || operations.get(app.id) === "saving" || operations.get(app.id) === "deleting" ? "muted" : app.enabled ? "success" : "muted"}`}
                                  >
                                    <i />
                                    {appStatus(app)}
                                  </span>
                                </td>
                                <td className="date-cell">
                                  {date(app.updated_at)}
                                </td>
                                <td>
                                  <button
                                    className="icon-button"
                                    aria-label={`Manage ${app.name}`}
                                    onClick={() =>
                                      navigate(() => setSelected(app.id))
                                    }
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
              <span>Your applications. Your credentials.</span>
            </footer>
          </div>
        </div>
      )}
      {oauthRedirecting && oauthProvider !== undefined && (
        <div className="oauth-navigation" role="status">
          <span>Opening {providerNames[oauthProvider]}…</span>
          <button className="button secondary" onClick={cancelOAuth}>
            Cancel sign-in
          </button>
        </div>
      )}
      {editor !== undefined && session === "signed-in" && (
        <AppEditor
          app={editor}
          providers={providers}
          onClose={closeEditor}
          onSave={saveApp}
          onUnauthorized={reportError}
        />
      )}
      {secretEditor !== undefined && session === "signed-in" && (
        <SecretEditor
          value={
            secretEditor.secret === "new"
              ? { name: "", description: "" }
              : secretEditor.secret
          }
          creating={secretEditor.secret === "new"}
          onClose={closeSecretEditor}
          onSave={saveSecret}
          onUnauthorized={reportError}
        />
      )}
      {credential !== undefined && (
        <CredentialDialog credential={credential} onClose={closeCredential} />
      )}
      {confirmation !== undefined && session === "signed-in" && (
        <ConfirmDialog
          confirmation={confirmation}
          onClose={closeConfirmation}
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
