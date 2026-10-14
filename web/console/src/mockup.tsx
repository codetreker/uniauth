import { useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { Console } from "./Console";
import { createMockAPI } from "./mock-api";
import type { PreviewPersona } from "./mock-api";
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
