/// <reference types="vite/client" />

// The one environment variable this front end reads. It is declared rather
// than left to Vite's index signature so a typo in the name is a type
// error: an unknown key would otherwise be `string | undefined` like a
// real one, and the app would quietly fall back to the default forever.
interface ImportMetaEnv {
  readonly VITE_API_BASE_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
