import { defineConfig } from "vite"
import react from "@vitejs/plugin-react-swc"
import tailwindcss from "@tailwindcss/vite"
import path from "node:path"

// SWC keeps HMR fast; Tailwind v4 runs as a Vite plugin (no PostCSS config).
// Wails serves frontend/dist from the embedded FS.
export default defineConfig({
	plugins: [react(), tailwindcss()],
	resolve: {
		alias: { "@": path.resolve(import.meta.dirname, "./src") },
	},
	server: { port: 5173, strictPort: true },
	build: { outDir: "dist", target: "es2022", sourcemap: false },
})
