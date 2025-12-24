// front/app-knowledge-console/next.config.ts
const nextConfig = {
    output: 'standalone',
    experimental: {
        turbopackUseSystemTlsCerts: true,
    },
    eslint: {
        // Warning: This allows production builds to successfully complete even if
        // your project has ESLint errors.
        ignoreDuringBuilds: true,
    },
    typescript: {
        ignoreBuildErrors: false,
        tsconfigPath: 'tsconfig.json',
    },
};

export default nextConfig;
