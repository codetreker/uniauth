import { createRoot } from "react-dom/client";
import { Console } from "./Console";
import { createMockAPI } from "./mock-api";
import "./styles.css";

const root = document.getElementById("root");
if (root === null) throw new Error("Console root element is missing.");
createRoot(root).render(<Console api={createMockAPI()} preview />);
