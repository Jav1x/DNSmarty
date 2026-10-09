import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { App } from "./App";
import { I18nProvider } from "./i18n";
// Шрифты «Лёд+»: переменные, с кириллицей (спец §3). index.css = latin+cyrillic ext/обычные.
import "@fontsource-variable/jetbrains-mono";
import "@fontsource-variable/manrope";
// Стили: tokens (спец §3) → старые слои (до их разбора в задачах 3–12) → ui.css последним.
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/components.css";
import "./styles/pages.css";
import "./styles/ui.css";

createRoot(document.getElementById("root")).render(
  <StrictMode>
    <I18nProvider>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </I18nProvider>
  </StrictMode>,
);
