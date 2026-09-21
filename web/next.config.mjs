/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  // The Go API is a separate origin in development; nothing is proxied so the
  // Authorization header travels straight to it (see NEXT_PUBLIC_API_URL).
  eslint: {
    dirs: ['src'],
  },
  // A production build and `next dev` share .next by default, and the build
  // overwrites manifests the dev server is still reading — after which every
  // page answers 500 until .next is deleted and dev restarted.
  //
  // `npm run build:check` sets this so a verification build lands somewhere
  // else and leaves a running dev server alone. Unset, behaviour is stock.
  ...(process.env.NEXT_DIST_DIR ? { distDir: process.env.NEXT_DIST_DIR } : {}),
  // The Docker image runs the self-contained server Next writes under
  // .next/standalone, which needs no node_modules at runtime. Only set by
  // web/Dockerfile; a local `next dev` or `next build` is unchanged.
  ...(process.env.NEXT_OUTPUT === 'standalone' ? { output: 'standalone' } : {}),
};

export default nextConfig;
