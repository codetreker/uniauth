import { useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { Console } from "./Console";
import { createMockAPI } from "./mock-api";
import type { PreviewMicrosoftChoice, PreviewPersona } from "./mock-api";
import "./styles.css";

const Mockup = () => {
  const [persona, setPersona] = useState<PreviewPersona>("administrator");
  const [signedOut, setSignedOut] = useState(false);
  const api = useMemo(
    () => createMockAPI(persona, signedOut),
    [persona, signedOut],
  );
  return (
    <>
      <div className="preview-toolbar">
        <label>
          Preview account
          <select
            aria-label="Preview account"
            value={persona}
            onChange={(event) =>
              setPersona(event.target.value as PreviewPersona)
            }
          >
            <option value="administrator">Platform administrator</option>
            <option value="developer">Product developer</option>
            <option value="new-account">New account (missing profile)</option>
          </select>
        </label>
        <label className="preview-login-toggle">
          <input
            type="checkbox"
            checked={signedOut}
            onChange={(event) => setSignedOut(event.target.checked)}
          />
          Start signed out
        </label>
        <label>
          Microsoft identity
          <select
            key={`${persona}:${signedOut}`}
            aria-label="Preview Microsoft identity"
            defaultValue="personal"
            onChange={(event) =>
              api.setMicrosoftChoice(
                event.target.value as PreviewMicrosoftChoice,
              )
            }
          >
            <option value="personal">Personal</option>
            <option value="organization-one">
              Organization one (same email)
            </option>
            <option value="organization-two">
              Organization two (missing profile)
            </option>
            <option value="conflict">Owned by another account</option>
            <option value="cancel">Cancel</option>
            <option value="failure">Authentication failure</option>
          </select>
        </label>
        <span>Interactive · Browser-only data</span>
      </div>
      <Console
        key={`${persona}:${signedOut}`}
        api={api}
        preview
        onAuthorization={api.completeAuthorization}
      />
    </>
  );
};
const root = document.getElementById("root");
if (root === null) throw new Error("Console root element is missing.");
createRoot(root).render(<Mockup />);
