import { createRoot } from "react-dom/client";
import { Console } from "./Console";
import { createAPI } from "./api";
import "./styles.css";

const root = document.getElementById("root");
if (root === null) throw new Error("Console root element is missing.");
createRoot(root).render(<Console api={createAPI()} />);
