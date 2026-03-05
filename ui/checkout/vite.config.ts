import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: '../../internal/ui/assets/checkout',
    emptyOutDir: true,
    rollupOptions: {
      input: {
        checkout: 'src/main.ts',
        subscribe: 'src/subscribe-main.ts',
      },
      output: {
        entryFileNames: '[name].js',
        assetFileNames: '[name][extname]',
      },
    },
  },
});
