import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// API target is configurable so the same config works locally and in Docker.
const target = process.env.VITE_API_TARGET || "http://localhost:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target, changeOrigin: true },
    },
  },
});
