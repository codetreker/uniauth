import { useEffect, useRef, useState } from "react";
import { APIError } from "./api";
import type {
  Account,
  ConsoleAPI,
  LinkedIdentity,
  Provider,
  ProviderID,
  UserSummary,
} from "./api";
import {
  CopyValue,
  ErrorNotice,
  Icon,
  Loading,
  ProviderMark,
  providerNames,
} from "./ui";
import type { OAuthOutcome } from "./ui";

export const accountName = (account: Pick<Account, "name">) =>
  account.name === null ? "Unnamed account" : account.name;
const date = (value: string) =>
  new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    year: "numeric",
  }).format(new Date(value));
const message = (cause: unknown) =>
  cause instanceof Error ? cause.message : "An unexpected error occurred.";
export const AccountAvatar = ({ user }: { user: Account }) =>
  user.avatar_url === null ? (
    <span className="avatar small" aria-label="No profile image">
      <Icon name="shield" size={18} />
    </span>
  ) : (
    <img
      className="avatar small account-avatar"
      src={user.avatar_url}
      alt=""
      referrerPolicy="no-referrer"
    />
  );

export const AccountView = ({
  api,
  user,
  providers,
  blocked,
  connecting,
  onLink,
  onUnauthorized,
}: {
  api: ConsoleAPI;
  user: Account;
  providers: Provider[];
  blocked: boolean;
  connecting?: ProviderID;
  onLink: (provider: ProviderID) => Promise<OAuthOutcome>;
  onUnauthorized: (cause: unknown) => void;
}) => {
  const [identities, setIdentities] = useState<LinkedIdentity[]>();
  const [error, setError] = useState<string>();
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    let active = true;
    setIdentities(undefined);
    setError(undefined);
    api
      .listIdentities()
      .then((result) => {
        if (active) setIdentities(result);
      })
      .catch((cause: unknown) => {
        if (!active) return;
        setError(message(cause));
        if (cause instanceof APIError && cause.status === 401)
          onUnauthorized(cause);
      });
    return () => {
      active = false;
    };
  }, [api, revision, user.id]);
  const link = async (provider: ProviderID) => {
    if (blocked || connecting !== undefined) return;
    setError(undefined);
    try {
      if ((await onLink(provider)) === "completed")
        setRevision((value) => value + 1);
    } catch (cause) {
      setError(message(cause));
      if (cause instanceof APIError && cause.status === 401)
        onUnauthorized(cause);
    }
  };
  return (
    <>
      <div className="page-heading">
        <div>
          <span className="eyebrow">YOUR IDENTITY</span>
          <h1>My account</h1>
          <p>Manage the sign-in identities that belong to your account.</p>
        </div>
      </div>
      <section className="panel account-profile">
        <AccountAvatar user={user} />
        <div>
          <h2>{accountName(user)}</h2>
          <p>{user.email === null ? "Email not provided" : user.email}</p>
          <span className="badge muted">
            {user.is_admin ? "Platform administrator" : "Platform member"}
          </span>
        </div>
      </section>
      <section className="panel account-details">
        <h2>Account details</h2>
        <dl className="detail-list">
          <div>
            <dt>Account ID</dt>
            <dd>
              <CopyValue label="Account ID" value={user.id} />
            </dd>
          </div>
          <div>
            <dt>Joined</dt>
            <dd>{date(user.created_at)}</dd>
          </div>
        </dl>
      </section>
      <section className="panel identities-panel">
        <div className="panel-heading">
          <div>
            <h2>Linked sign-in methods</h2>
            <p>
              Link another identity while signed in. Accounts are never combined
              by email.
            </p>
          </div>
        </div>
        {error !== undefined && <ErrorNotice message={error} />}
        {identities === undefined ? (
          error === undefined ? (
            <Loading label="Loading linked identities…" />
          ) : (
            <button
              className="button secondary"
              onClick={() => setRevision((value) => value + 1)}
            >
              Retry linked identities
            </button>
          )
        ) : (
          <>
            <div className="identity-list">
              {identities.map((identity) => (
                <article
                  className="identity-row"
                  key={`${identity.provider}:${identity.id}`}
                >
                  <ProviderMark provider={identity.provider} />
                  <div>
                    <strong>{providerNames[identity.provider]}</strong>
                    <p>
                      {identity.name === null
                        ? "Name not provided"
                        : identity.name}
                    </p>
                    <p>
                      {identity.email === null
                        ? "Email not provided"
                        : identity.email}
                      <span className="verification">
                        {identity.email_verified === null
                          ? "Verification not provided"
                          : identity.email_verified
                            ? "Verified email"
                            : "Unverified email"}
                      </span>
                    </p>
                    <code>{identity.id}</code>
                    <small>Linked {date(identity.linked_at)}</small>
                  </div>
                  <span className="badge success">Linked</span>
                </article>
              ))}
            </div>
            <div className="link-providers">
              {providers
                .filter((provider) => provider.available)
                .map(({ id }) => {
                  const linked = identities.some(
                    (identity) => identity.provider === id,
                  );
                  return (
                    <button
                      key={id}
                      className="button secondary"
                      disabled={blocked || connecting !== undefined || linked}
                      onClick={() => void link(id)}
                    >
                      <ProviderMark provider={id} />
                      {connecting === id
                        ? "Connecting…"
                        : linked
                          ? `${providerNames[id]} linked`
                          : `Link ${providerNames[id]}`}
                    </button>
                  );
                })}
            </div>
            {providers.every((provider) => !provider.available) && (
              <p className="hint">
                No sign-in providers are currently available for linking.
              </p>
            )}
          </>
        )}
      </section>
    </>
  );
};

export const UsersView = ({
  api,
  onUnauthorized,
}: {
  api: ConsoleAPI;
  onUnauthorized: (cause: unknown) => void;
}) => {
  const [users, setUsers] = useState<UserSummary[]>([]);
  const [cursor, setCursor] = useState<string | null>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const request = useRef(false);
  const generation = useRef(0);
  const load = async (next?: string) => {
    if (request.current) return;
    request.current = true;
    const current = generation.current;
    setLoading(true);
    setError(undefined);
    try {
      const page = await api.listUsers(next);
      if (current !== generation.current) return;
      setUsers((previous) =>
        next === undefined
          ? page.users
          : [
              ...previous,
              ...page.users.filter(
                (user) => !previous.some((item) => item.id === user.id),
              ),
            ],
      );
      setCursor(page.next_cursor);
    } catch (cause) {
      if (current !== generation.current) return;
      setError(message(cause));
      if (cause instanceof APIError && cause.status === 401)
        onUnauthorized(cause);
    } finally {
      if (current === generation.current) {
        request.current = false;
        setLoading(false);
      }
    }
  };
  useEffect(() => {
    void load();
    return () => {
      generation.current += 1;
      request.current = false;
    };
  }, [api]);
  return (
    <>
      <div className="page-heading">
        <div>
          <span className="eyebrow">PLATFORM OVERVIEW</span>
          <h1>Platform users</h1>
          <p>
            Account summaries and application counts. Each member manages their
            own applications.
          </p>
        </div>
      </div>
      <section className="panel users-panel">
        <div className="panel-heading">
          <div>
            <h2>Registered users</h2>
            <p>Profile information shared by each member's sign-in provider.</p>
          </div>
        </div>
        {error !== undefined && <ErrorNotice message={error} />}
        {users.length > 0 && (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>User</th>
                  <th>Created</th>
                  <th>Applications</th>
                </tr>
              </thead>
              <tbody>
                {users.map((user) => (
                  <tr key={user.id}>
                    <td>
                      <div className="user-summary">
                        <strong>{accountName(user)}</strong>
                        <span>
                          {user.email === null
                            ? "Email not provided"
                            : user.email}
                        </span>
                        <code>{user.id}</code>
                      </div>
                    </td>
                    <td>{date(user.created_at)}</td>
                    <td>
                      <span className="user-app-count">{user.app_count}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {loading ? (
          <Loading label="Loading users…" />
        ) : error !== undefined ? (
          <button
            className="button secondary"
            onClick={() =>
              void load(
                cursor === undefined || cursor === null ? undefined : cursor,
              )
            }
          >
            Retry users
          </button>
        ) : users.length === 0 ? (
          <div className="compact-empty">
            <p>No users found.</p>
          </div>
        ) : (
          cursor !== null &&
          cursor !== undefined && (
            <button
              className="button secondary load-more"
              onClick={() => void load(cursor)}
            >
              Load more users
            </button>
          )
        )}
      </section>
    </>
  );
};
