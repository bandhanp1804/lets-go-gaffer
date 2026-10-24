// The browser only ever talks to this origin; /api/* is forwarded to the Go server.
// That keeps the API free of CORS headers and keeps keys out of the browser.
const api = process.env.GAFFER_API ?? "http://127.0.0.1:8080";

/** @type {import('next').NextConfig} */
const nextConfig = {
  poweredByHeader: false,
  compress: false, // local only, and compression can buffer the answer stream
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${api}/api/:path*` }];
  },
};

export default nextConfig;
