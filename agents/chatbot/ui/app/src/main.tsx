import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
// The kit's panel and shell styles, then this application's own: the app sheet
// overrides where a domain panel sits beside a kit one (srd004 R4).
import "@declarative-agents/ui-kit/styles.css";
import "./App.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
