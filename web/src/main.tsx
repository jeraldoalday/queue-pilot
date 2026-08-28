import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";

import { App } from "./App";
import "./styles.css";

const root = document.getElementById("root");
if (!root) {
  // index.html owns this element, so a missing one is a broken build
  // rather than a runtime condition to handle. Saying so is more useful
  // than the "Cannot read properties of null" React would raise a line
  // later.
  throw new Error("#root is missing from index.html");
}

createRoot(root).render(
  <StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </StrictMode>,
);
