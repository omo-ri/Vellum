import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./App";

const container = document.getElementById("root");
if (!container) {
  throw new Error("index.html 里找不到 #root");
}

createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
