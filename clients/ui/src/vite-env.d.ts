/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Base URL of the hub-shim; default http://127.0.0.1:7080. */
  readonly VITE_SHIM_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
