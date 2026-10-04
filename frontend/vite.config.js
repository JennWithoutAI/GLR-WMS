import { defineConfig } from 'vite'

export default defineConfig({
    root: 'app',               // folder containing index.html
    server: {
        port: 80,
        host: true
    },
    build: {
        outDir: '../dist',     // output goes outside the root
        emptyOutDir: true
    }
})