import { useEffect, useId, useRef, useState } from "react";
import type { ReactNode } from "react";
import type { ProviderID } from "./api";

export type OAuthOutcome = "blocked" | "redirecting" | "completed";

export const providerNames: Record<ProviderID, string> = {
  microsoft: "Microsoft",
  google: "Google",
  github: "GitHub",
};
export const ProviderMark = ({ provider }: { provider: ProviderID }) => (
  <span className={`provider-mark ${provider}`} aria-hidden="true">
    {provider === "github" ? (
      <svg viewBox="0 0 24 24">
        <path
          fill="currentColor"
          d="M12 2a10 10 0 0 0-3.16 19.49c.5.1.68-.22.68-.48v-1.87c-2.78.6-3.37-1.18-3.37-1.18-.45-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.61.07-.61 1 .07 1.53 1.03 1.53 1.03.9 1.52 2.34 1.08 2.91.83.1-.65.35-1.09.63-1.34-2.22-.25-4.55-1.11-4.55-4.94 0-1.09.39-1.99 1.03-2.69-.1-.26-.45-1.28.1-2.66 0 0 .84-.27 2.75 1.03a9.6 9.6 0 0 1 5 0c1.91-1.3 2.75-1.03 2.75-1.03.55 1.38.2 2.4.1 2.66.64.7 1.03 1.6 1.03 2.69 0 3.84-2.34 4.69-4.57 4.94.36.31.68.92.68 1.85v2.73c0 .27.18.59.69.49A10 10 0 0 0 12 2Z"
        />
      </svg>
    ) : provider === "google" ? (
      <b>G</b>
    ) : (
      <span className="ms-squares">
        <i />
        <i />
        <i />
        <i />
      </span>
    )}
  </span>
);

export const Icon = ({
  name,
  size = 20,
}: {
  name:
    | "apps"
    | "key"
    | "arrow"
    | "plus"
    | "logout"
    | "close"
    | "copy"
    | "check"
    | "shield"
    | "search"
    | "trash";
  size?: number;
}) => {
  const paths = {
    apps: (
      <>
        <rect x="3" y="3" width="7" height="7" rx="1.5" />
        <rect x="14" y="3" width="7" height="7" rx="1.5" />
        <rect x="3" y="14" width="7" height="7" rx="1.5" />
        <rect x="14" y="14" width="7" height="7" rx="1.5" />
      </>
    ),
    key: (
      <>
        <circle cx="8" cy="8" r="5" />
        <path d="m11.5 11.5 9 9M17 17l3-3m-6 0 3-3" />
      </>
    ),
    arrow: <path d="m9 6 6 6-6 6" />,
    plus: <path d="M12 5v14M5 12h14" />,
    logout: (
      <>
        <path d="M10 4H4v16h6M9 12h12m-4-4 4 4-4 4" />
      </>
    ),
    close: <path d="m6 6 12 12M6 18 18 6" />,
    copy: (
      <>
        <rect x="8" y="8" width="12" height="12" rx="2" />
        <path d="M16 8V4H4v12h4" />
      </>
    ),
    check: <path d="m5 12 4 4L19 6" />,
    shield: (
      <>
        <path d="M12 3 4 6v6c0 5 8 9 8 9s8-4 8-9V6Z" />
        <path d="m8 12 3 3 5-6" />
      </>
    ),
    search: (
      <>
        <circle cx="10.5" cy="10.5" r="6.5" />
        <path d="m16 16 5 5" />
      </>
    ),
    trash: (
      <>
        <path d="M3 6h18M9 6V3h6v3M6 6l1 15h10l1-15M10 10v7m4-7v7" />
      </>
    ),
  };
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {paths[name]}
    </svg>
  );
};
export const Brand = () => (
  <span className="brand">
    <span className="brand-symbol">
      <Icon name="shield" size={24} />
    </span>
    <span>
      UniAuth<span className="brand-sub">Console</span>
    </span>
  </span>
);

export const Modal = ({
  title,
  children,
  onClose,
  locked = false,
  wide = false,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  locked?: boolean;
  wide?: boolean;
}) => {
  const titleID = useId();
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const element = dialog.current;
    if (element === null) return;
    element.showModal();
    return () => element.close();
  }, []);
  return (
    <dialog
      ref={dialog}
      className={`modal ${wide ? "wide" : ""}`}
      aria-labelledby={titleID}
      onCancel={(event) => {
        event.preventDefault();
        if (!locked) onClose();
      }}
    >
      <div className="modal-title">
        <h2 id={titleID}>{title}</h2>
        {!locked && (
          <button
            type="button"
            className="icon-button"
            aria-label="Close dialog"
            onClick={onClose}
          >
            <Icon name="close" />
          </button>
        )}
      </div>
      {children}
    </dialog>
  );
};
export const ErrorNotice = ({ message }: { message: string }) => (
  <div className="notice error" role="alert">
    <strong>Something needs attention</strong>
    <span>{message}</span>
  </div>
);
export const Loading = ({ label = "Loading…" }: { label?: string }) => (
  <div className="loading" role="status">
    <span className="spinner" />
    {label}
  </div>
);
export const CopyValue = ({
  label,
  value,
}: {
  label: string;
  value: string;
}) => {
  const [status, setStatus] = useState<"idle" | "copied" | "failed">("idle");
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setStatus("copied");
    } catch {
      setStatus("failed");
    }
  };
  return (
    <div className="copy-field">
      <span className="field-label">{label}</span>
      <div>
        <code tabIndex={0}>{value}</code>
        <button
          type="button"
          className="icon-button"
          aria-label={`Copy ${label}`}
          onClick={copy}
        >
          <Icon name={status === "copied" ? "check" : "copy"} />
        </button>
      </div>
      <span className="copy-status" role="status">
        {status === "copied"
          ? "Copied to clipboard."
          : status === "failed"
            ? "Clipboard unavailable. Select and copy the value above."
            : ""}
      </span>
    </div>
  );
};
