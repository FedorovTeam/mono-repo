// front/app-knowledge-console/types/next-auth.d.ts
import type { JWT as DefaultJWT } from "next-auth/jwt";

declare module "next-auth" {
  interface Session {
    accessToken?: string;
    idToken?: string;
    expiresAt?: number;
    nextRefreshAttempt?: number;
    reauthRequired?: boolean;
    user?: {
      name?: string | null;
      email?: string | null;
    };
  }
}

declare module "next-auth/jwt" {
  interface JWT extends DefaultJWT {
    accessToken?: string;
    idToken?: string;
    refreshToken?: string;
    expiresAt?: number;
    profile?: Record<string, unknown> | null;
    refreshAttempts?: number;
    nextRefreshAttempt?: number;
    reauthRequired?: boolean;
  }
}

export {};
