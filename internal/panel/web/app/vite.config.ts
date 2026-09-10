import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

// Собранная панель кладётся в web/dist и вшивается в бинарник через go:embed.
// Это главное решение: приложение полноценно реактовое, но деплой остаётся
// одним файлом — steno ставится на сервер копированием, без node в проде.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": path.resolve(__dirname, "src") } },
  build: {
    outDir: path.resolve(__dirname, "../dist"),
    emptyOutDir: true,
  },
  server: {
    port: 5273,
    // В разработке фронт живёт отдельно и ходит в запущенный steno serve.
    proxy: {
      "/api": { target: "http://127.0.0.1:8331", changeOrigin: true },
      "/audio": { target: "http://127.0.0.1:8331", changeOrigin: true },
    },
  },
});
