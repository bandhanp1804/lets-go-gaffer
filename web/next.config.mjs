// The browser only ever talks to this origin; /api/* is forwarded to the Go server.
// That keeps the API free of CORS headers and keeps keys out of the browser.
const api = process.env.GAFFER_API ?? "http://127.0.0.1:8080";

/** @type {import('next').NextConfig} */
const nextConfig = {
  // Test builds can go to their own folder (GAFFER_DIST=.next-test) so they never touch the one a
  // running `next dev` is using.
  distDir: process.env.GAFFER_DIST ?? ".next",
  // A self-contained server for the container image (deploy/docker/Dockerfile.web). In the
  // cluster the ingress sends /api straight to the API, so the rewrite below is for local runs.
  output: "standalone",
  poweredByHeader: false,
  compress: false, // local only, and compression can buffer the answer stream
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${api}/api/:path*` }];
  },
};

export default nextConfig;
