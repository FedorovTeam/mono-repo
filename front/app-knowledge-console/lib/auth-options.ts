import type { NextAuthOptions } from "next-auth";
import OAuthProvider from "next-auth/providers/oauth";

const authentikIssuer = process.env.AUTHENTIK_ISSUER;
const authentikClientId = process.env.AUTHENTIK_CLIENT_ID;
const authentikClientSecret = process.env.AUTHENTIK_CLIENT_SECRET;
const nextAuthSecret = process.env.NEXTAUTH_SECRET;
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

const authorizationParams: Record<string, string> = { scope: "openid email profile" };
if (authentikRedirectUri) {
  authorizationParams.redirect_uri = authentikRedirectUri;
}

export const authOptions: NextAuthOptions = {
  secret: nextAuthSecret,
  providers: [
    OAuthProvider({
      id: "authentik",
      name: "Authentik",
      type: "oidc",
      issuer: authentikIssuer,
      wellKnown: process.env.AUTHENTIK_WELL_KNOWN ?? `${authentikIssuer}/.well-known/openid-configuration`,
      jwks_endpoint: process.env.AUTHENTIK_JWKS_URL,
      clientId: authentikClientId,
      clientSecret: authentikClientSecret,
      authorization: {
        params: authorizationParams,
      },
      idToken: true,
      checks: ["pkce", "state"],
      profile(profile) {
        return {
          id: profile.sub ?? profile.email ?? "authentik-user",
          name: profile.name ?? profile.preferred_username ?? profile.email,
          email: profile.email ?? null,
        };
      },
    }),
  ],
  session: {
    strategy: "jwt",
  },
  callbacks: {
    async jwt({ token, account, profile }) {
      if (account) {
        token.accessToken = account.access_token;
        token.idToken = account.id_token;
        token.refreshToken = account.refresh_token;
        token.expiresAt = account.expires_at ? account.expires_at * 1000 : undefined;
        token.profile = profile ?? token.profile;
      }

      return token;
    },
    async session({ session, token }) {
      session.accessToken = typeof token.accessToken === "string" ? token.accessToken : undefined;
      session.idToken = typeof token.idToken === "string" ? token.idToken : undefined;

      if (session.user) {
        const profile = token.profile && typeof token.profile === "object" ? token.profile : null;
        const profileName = profile ? (profile as Record<string, unknown>).name : undefined;
        const profileEmail = profile ? (profile as Record<string, unknown>).email : undefined;

        session.user.name = session.user.name ?? (typeof profileName === "string" ? profileName : undefined);
        session.user.email = session.user.email ?? (typeof profileEmail === "string" ? profileEmail : undefined);
      }

      return session;
    },
  },
};
