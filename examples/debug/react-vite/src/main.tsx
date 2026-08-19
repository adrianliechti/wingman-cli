import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

function App() {
  const message = "hello from React/Vite";
  return <main>{message}</main>;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
