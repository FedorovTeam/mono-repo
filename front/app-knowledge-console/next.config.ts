// front/app-knowledge-console/next.config.ts
const nextConfig = {
    output: 'standalone',
    experimental: {
        turbopackUseSystemTlsCerts: true,
    },
    typescript: {
        ignoreBuildErrors: false,
        tsconfigPath: 'tsconfig.json',
    },
};

export default nextConfig;
