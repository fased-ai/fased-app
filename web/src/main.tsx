import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { initDesktopRuntime } from "./desktop-runtime";
import "./styles.css";
import "./vendor/wen-design/style.css";
import "./primitives.css";
import "./wen-app.css";
import "./shell.css";
import "./dashboard.css";
initDesktopRuntime();
ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
