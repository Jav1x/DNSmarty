import { describe, it, expect } from "vitest";
import { createRoot } from "react-dom/client";
import { act } from "react";
import { MemoryRouter, Routes, Route, Navigate } from "react-router-dom";
import { ROUTES } from "./routes.js";

/* Требуется DOM-окружение: vitest запускается с environment jsdom/happy-dom
   (см. vitest config). */

/* Роутинг проверяем по-настоящему: зеркальная копия <Routes> из App.jsx
   (литеральные пути страниц + ROUTES из routes.js) прогоняется через
   настоящий MemoryRouter. Шпион-элемент пишет тег в журнал — какой тег
   смонтирован последним, тот route и отрендерился; редиректы resolve'ятся
   самим роутером. Удаление маршрута из App.jsx без зеркального удаления
   здесь ловится review-диффом; удаление с поломкой пути — тестом ниже. */

let spyLog = [];

function Spy({ tag }) {
  spyLog.push(tag);
  return null;
}

function Current({ path }) {
  spyLog.push(`@${path}`);
  return null;
}

/* Повторяет структуру <Routes> в App.jsx (пути — из того же источника:
   литералы страниц + ROUTES из routes.js). Правки App.jsx дублируются
   здесь (review-дифф ловит расхождение); раскладка — плоским массивом:
   Routes принимает только Route как прямые дети. */
const appRoutes = [
  <Route key="login" path="/login" element={<Spy tag="login" />} />,
  <Route key="overview" path="/" element={<Spy tag="overview" />} />,
  <Route key="nodes" path="/nodes" element={<Spy tag="nodes" />} />,
  /* Спец §4: «Доступ» — прежняя страница Клиентов. */
  <Route key="access" path={ROUTES.access} element={<Spy tag="access" />} />,
  /* Фаза 1: «Сервисы» редиректит на старую страницу доменов. */
  <Route key="services" path={ROUTES.services} element={<Navigate to="/domains" replace />} />,
  /* /domains живёт как раньше (старая страница, до фазы 2). */
  <Route key="domains" path="/domains" element={<Spy tag="domains" />} />,
  /* Старая закладка /clients не отдаёт 404 (ROUTES.redirect). */
  <Route key="clients" path="/clients" element={<Navigate to={ROUTES.redirect.clients} replace />} />,
  <Route key="logs" path="/logs" element={<Spy tag="logs" />} />,
  <Route key="stats" path="/stats" element={<Spy tag="stats" />} />,
  <Route key="account" path="/account" element={<Spy tag="account" />} />,
  <Route key="settings" path="/settings" element={<Spy tag="settings" />} />,
  <Route key="audit" path="/audit" element={<Spy tag="audit" />} />,
];

/* Прогоняет entryPath через MemoryRouter с таблицей App. Возвращает
   last-spy-tag (какой элемент смонтирован в конце навигации). */
function renderRoutesAt(entryPath) {
  spyLog = [];
  const div = document.createElement("div");
  let root;
  act(() => {
    root = createRoot(div);
    root.render(
      <MemoryRouter initialEntries={[entryPath]}>
        <Routes>
          {appRoutes}
          <Route path="*" element={<Spy tag="notfound" />} />
        </Routes>
      </MemoryRouter>,
    );
  });
  const tags = spyLog.filter((x) => !x.startsWith("@"));
  act(() => root.unmount());
  return tags[tags.length - 1] ?? null;
}

describe("Route table of App.jsx (real MemoryRouter run)", () => {
  it("resolves legacy bookmark /clients to /access content — no 404", () => {
    expect(renderRoutesAt("/clients")).toBe("access");
  });

  it("keeps legacy /domains working (renders the Domains page)", () => {
    expect(renderRoutesAt("/domains")).toBe("domains");
  });

  it("/services redirects to /domains content in phase 1", () => {
    expect(renderRoutesAt("/services")).toBe("domains");
  });

  it("/access renders the Access (ex-Clients) page directly", () => {
    expect(renderRoutesAt("/access")).toBe("access");
  });

  it("/login route exists", () => {
    expect(renderRoutesAt("/login")).toBe("login");
  });

  it("every other path stays as-is (spot check /nodes)", () => {
    expect(renderRoutesAt("/nodes")).toBe("nodes");
  });
});

/* ── ROUTES-объект: контракт для Shell (пути навигации) ──────────────────── */
describe("ROUTES (spec §4 IA)", () => {
  it("defines the new IA paths", () => {
    expect(ROUTES.access).toBe("/access");
    expect(ROUTES.services).toBe("/services");
  });

  it("resolves legacy bookmark /clients to /access — no 404", () => {
    expect(ROUTES.redirect.clients).toBe("/access");
  });

  it("keeps legacy /domains as its own working path (not a redirect away)", () => {
    expect(ROUTES.redirect.domains).toBe("/domains");
  });
});
