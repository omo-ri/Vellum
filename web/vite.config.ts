import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    // 端口被占用时直接失败，而不是悄悄换到 5174——否则 proxy 与书签都会指向一个
    // 已经不在那儿的服务。
    strictPort: true,
    proxy: {
      // 前端只认相对路径 /api，跨源问题因此在开发期就不存在，
      // 也就不需要 CORS 中间件。生产上由 Go 二进制自己同时提供 SPA 与 /api。
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: false,
      },
    },
    watch: {
      // 代码在 /mnt/z（9p）上，inotify 在这里不工作——不轮询就完全收不到文件变更。
      // 1 秒的间隔是在「保存后多久看到效果」与「持续扫目录的开销」之间取的折中。
      usePolling: true,
      interval: 1000,
    },
  },
});
