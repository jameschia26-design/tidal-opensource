import next from "eslint-config-next";

// React Compiler's set-state-in-effect rule fires on the app's load-on-company-change pattern
// (reset state, then fetch). That pattern is guarded by liveness flags and is not a defect, so the
// rule runs as a warning while the screens are migrated to suspense-friendly loading.
const config = [
  ...next,
  { ignores: [".next/**", "node_modules/**"] },
  { rules: { "react-hooks/set-state-in-effect": "warn" } },
];

export default config;
