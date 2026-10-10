/* Route map of the panel — spec §4 (IA of the redesign).
   App.jsx builds react-router routes from it; the test (App.routes.test.jsx)
   pins the contract: new paths + legacy bookmark redirects (never 404). */
export const ROUTES = {
  access: "/access",
  services: "/services",
  // Legacy bookmarks: /clients → /access; /domains → /services.
  redirect: {
    clients: "/access",
    domains: "/services",
  },
};
