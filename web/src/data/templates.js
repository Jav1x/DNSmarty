export const BUILTIN_TEMPLATES = [
  {
    id: "builtin:xbox-live",
    name: "Xbox Live",
    payload: {
      domains: [{ name: "auth.xboxlive.com", match: "suffix" }],
      strategy: "round_robin",
      proxies: [],
    },
  },
];
