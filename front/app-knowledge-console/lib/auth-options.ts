import type { NextAuthOptions } from "next-auth";
import type { OAuthConfig } from "next-auth/providers/oauth";
import type { JWT } from "next-auth/jwt";

const authentikIssuer = process.env.AUTHENTIK_ISSUER?.replace(/\/$/, "");
const authentikClientId = process.env.AUTHENTIK_CLIENT_ID;
const authentikClientSecret = process.env.AUTHENTIK_CLIENT_SECRET;
const nextAuthSecret = process.env.NEXTAUTH_SECRET;
const authentikTokenUrl = process.env.AUTHENTIK_TOKEN_URL ?? (authentikIssuer ? `${authentikIssuer}/token/` : undefined);
const authentikRedirectUri =
  process.env.AUTHENTIK_REDIRECT_URI ??
  (process.env.NEXTAUTH_URL ? `${process.env.NEXTAUTH_URL}/api/auth/callback/authentik` : undefined);

const requiredEnv = [
  ["AUTHENTIK_ISSUER", authentikIssuer],
  ["AUTHENTIK_CLIENT_ID", authentikClientId],
  ["AUTHENTIK_CLIENT_SECRET", authentikClientSecret],
  ["NEXTAUTH_SECRET", nextAuthSecret],
];

const missingEnv = requiredEnv.filter(([, value]) => !value).map(([key]) => key);

if (missingEnv.length > 0) {
  throw new Error(`Отсутствуют переменные окружения для аутентификации: ${missingEnv.join(", ")}`);
}

if (!authentikRedirectUri) {
  throw new Error("AUTHENTIK_REDIRECT_URI или NEXTAUTH_URL должны быть заданы для корректного redirect_uri");
}

if (!authentikTokenUrl) {
  throw new Error("Не удалось определить URL token endpoint для Authentik");
}

const authorizationParams: Record<string, string> = {
  scope: "openid email profile offline_access",
  access_type: "offline",
  prompt: "consent",
  ...(authentikRedirectUri ? { redirect_uri: authentikRedirectUri } : {}),
};

const authentikProvider: OAuthConfig<Record<string, unknown>> = {
  id: "authentik",
  name: "Authentik",
  type: "oauth",
  issuer: authentikIssuer,
  wellKnown: process.env.AUTHENTIK_WELL_KNOWN ?? `${authentikIssuer}/.well-known/openid-configuration`,
  jwks_endpoint: process.env.AUTHENTIK_JWKS_URL,
  clientId: authentikClientId,
  clientSecret: authentikClientSecret,
  authorization: { params: authorizationParams },
  idToken: true,
  checks: ["pkce", "state"],
  profile(profile) {
    const profileData = profile as Record<string, unknown>;
    const profileName = profileData.name ?? profileData.preferred_username ?? profileData.email;

    return {
      id:
        typeof profileData.sub === "string"
          ? profileData.sub
          : typeof profileData.email === "string"
            ? profileData.email
            : "authentik-user",
      name: typeof profileName === "string" ? profileName : undefined,
      email: typeof profileData.email === "string" ? profileData.email : null,
    };
  },
};

type ExtendedJWT = JWT & {
  accessToken?: string;
  idToken?: string;
  refreshToken?: string;
  expiresAt?: number;
  profile?: Record<string, unknown> | null;
};

type AuthentikTokenResponse = {
  access_token?: string;
  id_token?: string;
  refresh_token?: string;
  expires_in?: number;
};

const refreshLeewayMs = 2 * 60 * 1000;

const clearSession = (token: ExtendedJWT) => {
  token.accessToken = undefined;
  token.idToken = undefined;
  token.refreshToken = undefined;
  token.expiresAt = undefined;

  return token;
};

const refreshAccessToken = async (token: ExtendedJWT) => {
  if (!token.refreshToken || !token.expiresAt) {
    return token;
  }

  if (token.expiresAt - refreshLeewayMs > Date.now()) {
    return token;
  }

  try {
    const params = new URLSearchParams({
      grant_type: "refresh_token",
      refresh_token: token.refreshToken,
      client_id: authentikClientId ?? "",
      client_secret: authentikClientSecret ?? "",
      scope: "openid email profile",
    });

    const response = await fetch(authentikTokenUrl, {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
      },
      body: params.toString(),
    });

    if (!response.ok) {
      throw new Error(`Failed to refresh token: ${response.statusText}`);
    }

    const data = (await response.json()) as AuthentikTokenResponse;

    if (!data.access_token) {
      throw new Error("Authentik refresh response is missing access_token");
    }

    const updatedToken: ExtendedJWT = {
      ...token,
      accessToken: data.access_token,
      idToken: data.id_token ?? token.idToken,
      refreshToken: data.refresh_token ?? token.refreshToken,
      expiresAt: data.expires_in ? Date.now() + data.expires_in * 1000 : token.expiresAt,
    };

    return updatedToken;
  } catch (error) {
    console.error("Ошибка при обновлении токена Authentik", error);
    return clearSession(token);
  }
};

export const authOptions: NextAuthOptions = {
  secret: nextAuthSecret,
  providers: [authentikProvider],
  session: {
    strategy: "jwt",
  },
  pages: {
    signIn: "/auth/signin",
  },
  callbacks: {
    async jwt({ token, account, profile }) {
      const extendedToken = token as ExtendedJWT;
      const profileData =
        profile && typeof profile === "object" ? (profile as Record<string, unknown>) : null;

      if (account) {
        extendedToken.accessToken = account.access_token;
        extendedToken.idToken = account.id_token;
        extendedToken.refreshToken = account.refresh_token;
        extendedToken.expiresAt = account.expires_at ? account.expires_at * 1000 : undefined;
        extendedToken.profile = profileData ?? extendedToken.profile ?? null;
      }

      return await refreshAccessToken(extendedToken);
    },
    async session({ session, token }) {
      const extendedToken = token as ExtendedJWT;

      session.accessToken = typeof extendedToken.accessToken === "string" ? extendedToken.accessToken : undefined;
      session.idToken = typeof extendedToken.idToken === "string" ? extendedToken.idToken : undefined;
      session.expiresAt = extendedToken.expiresAt;

      if (session.user) {
        const profile =
          extendedToken.profile && typeof extendedToken.profile === "object" ? extendedToken.profile : null;
        const profileName = profile ? (profile as Record<string, unknown>).name : undefined;
        const profileEmail = profile ? (profile as Record<string, unknown>).email : undefined;

        session.user.name = session.user.name ?? (typeof profileName === "string" ? profileName : undefined);
        session.user.email = session.user.email ?? (typeof profileEmail === "string" ? profileEmail : undefined);
      }

      return session;
    },
  },
};
