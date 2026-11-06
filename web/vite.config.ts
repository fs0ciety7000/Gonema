/// <reference types="vitest/config" />
import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath, URL } from 'node:url';
import { brotliCompressSync, gzipSync, constants as zc } from 'node:zlib';
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig, type Plugin } from 'vite';

const repoRoot = fileURLToPath(new URL('..', import.meta.url));

/** Preloads the Latin font files so text renders in its final face on first paint. */
function preloadFonts(): Plugin {
  return {
    name: 'gonema-preload-fonts',
    apply: 'build',
    transformIndexHtml: {
      order: 'post',
      handler(_html, ctx) {
        const files = Object.keys(ctx.bundle ?? {}).filter((f) =>
          /(archivo-latin-wdth-normal|geist-latin-wght-normal|geist-mono-latin-wght-normal)[^/]*\.woff2$/.test(f),
        );
        return files.map((f) => ({
          tag: 'link',
          attrs: { rel: 'preload', href: `./${f}`, as: 'font', type: 'font/woff2', crossorigin: '' },
          injectTo: 'head' as const,
        }));
      },
    },
  };
}

/** gonema.css starts with `@import url("../../brand/kit/tokens.css")`; tokens.css is
 *  imported explicitly by app.css, so the nested (and unresolvable) import is removed. */
function dropNestedTokenImport(): Plugin {
  const re = /@import\s+(?:url\()?["']?\.\.\/\.\.\/brand\/kit\/tokens\.css["']?\)?\s*;/g;
  return {
    name: 'gonema-drop-nested-token-import',
    enforce: 'post',
    transform(code, id) {
      if (!/\.css($|\?)/.test(id) || !code.includes('brand/kit/tokens.css')) return null;
      return { code: code.replace(re, ''), map: null };
    },
    generateBundle(_opts, bundle) {
      for (const asset of Object.values(bundle)) {
        if (asset.type === 'asset' && asset.fileName.endsWith('.css') && typeof asset.source === 'string') {
          asset.source = asset.source.replace(re, '');
        }
      }
    },
  };
}

/** Precompressed siblings (.br, .gz) that internal/webui serves when the client accepts them. */
function precompress(): Plugin {
  return {
    name: 'gonema-precompress',
    apply: 'build',
    // after the files are written (emitting during generateBundle disturbs Vite's own rewrites)
    writeBundle(opts, bundle) {
      const dir = opts.dir ?? 'dist';
      for (const name of Object.keys(bundle)) {
        // index.html is excluded: the server injects <base href> and the runtime config into it.
        if (!/\.(js|css|svg|json|webmanifest)$/.test(name)) continue;
        const path = `${dir}/${name}`;
        const buf = readFileSync(path);
        if (buf.length < 1024) continue;
        writeFileSync(`${path}.gz`, gzipSync(buf, { level: 9 }));
        writeFileSync(`${path}.br`, brotliCompressSync(buf, { params: { [zc.BROTLI_PARAM_QUALITY]: 11 } }));
      }
    },
  };
}

/** The Go server injects <base href> (GONEMA_BASE_URL) into index.html; the dev server gets "/". */
function devBaseHref(): Plugin {
  return {
    name: 'gonema-dev-base-href',
    apply: 'serve',
    transformIndexHtml: () => [{ tag: 'base', attrs: { href: '/' }, injectTo: 'head-prepend' }],
  };
}

export default defineConfig(({ mode }) => ({
  // Relative base: the Go server may serve the SPA under GONEMA_BASE_URL.
  base: './',
  plugins: [react(), tailwindcss(), dropNestedTokenImport(), preloadFonts(), devBaseHref(), precompress()],
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  define: {
    // Dev-only screens (/__system, unbuilt sections) are compiled out of production builds.
    __GONEMA_DEV__: JSON.stringify(mode !== 'production' || process.env.GONEMA_DEV_SCREENS === '1'),
  },
  server: {
    port: 5173,
    fs: { allow: [repoRoot] },
    proxy: { '/api': { target: process.env.GONEMA_API ?? 'http://127.0.0.1:7676', changeOrigin: false } },
  },
  preview: {
    proxy: { '/api': { target: process.env.GONEMA_API ?? 'http://127.0.0.1:7676', changeOrigin: false } },
  },
  build: {
    target: 'es2023',
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
    assetsInlineLimit: 0,
    reportCompressedSize: true,
    rolldownOptions: {
      output: {
        // Everything the first screen imports statically ships as one chunk: lazy routes
        // sharing those modules otherwise split them into many small files, which gzip worse
        // and cost more requests (measured in e2e/perf.mjs).
        codeSplitting: { groups: [{ name: 'app', tags: ['$initial'] }] },
      },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./vitest.setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    css: false,
    restoreMocks: true,
  },
}));
