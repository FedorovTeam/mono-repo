import type { NextAuthOptions } from "next-auth";
import type { OAuthConfig } from "next-auth/providers/oauth";
import type { JWT } from "next-auth/jwt";
import https from "node:https";

type AuthentikAvailability = {
  status: "unknown" | "reachable" | "unreachable";
  checkedAt: number | null;
  error?: string;
};

const authentikIssuer = process.env.AUTHENTIK_ISSUER?.replace(/\/$/, "");
const authentikClientId = process.env.AUTHENTIK_CLIENT_ID;
const authentikClientSecret = process.env.AUTHENTIK_CLIENT_SECRET;
const nextAuthSecret = process.env.NEXTAUTH_SECRET;
const authentikTokenUrl = process.env.AUTHENTIK_TOKEN_URL ?? (authentikIssuer ? `${authentikIssuer}/token/` : undefined);
const authentikRedirectUri =
  process.env.AUTHENTIK_REDIRECT_URI ??
  (process.env.NEXTAUTH_URL ? `${process.env.NEXTAUTH_URL}/api/auth/callback/authentik` : undefined);
const authentikHttpTimeoutMs = Math.max(
  1000,
  Number.isFinite(Number(process.env.AUTHENTIK_HTTP_TIMEOUT_MS))
    ? Number(process.env.AUTHENTIK_HTTP_TIMEOUT_MS)
    : 12000,
);
const authentikHttpAgent = new https.Agent({
  keepAlive: true,
  keepAliveMsecs: 1000,
  maxSockets: 50,
});

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
  ...(authentikRedirectUri ? { redirect_uri: authentikRedirectUri } : {}),
};

const authentikDiscoveryUrl = (() => {
  const url = process.env.AUTHENTIK_WELL_KNOWN ?? `${authentikIssuer}/.well-known/openid-configuration`;

  try {
    return new URL(url).toString();
  } catch (error) {
    console.warn("[auth-options] Некорректный URL discovery для Authentik", { url, error });
    return url;
  }
})();

const authentikProvider: OAuthConfig<Record<string, unknown>> = {
  id: "authentik",
  name: "Authentik",
  type: "oauth",
  issuer: authentikIssuer,
  wellKnown: authentikDiscoveryUrl,
  jwks_endpoint: process.env.AUTHENTIK_JWKS_URL,
  clientId: authentikClientId,
  clientSecret: authentikClientSecret,
  authorization: { params: authorizationParams },
  idToken: true,
  checks: ["pkce", "state"],
  httpOptions: {
    timeout: authentikHttpTimeoutMs,
    agent: authentikHttpAgent,
  },
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

const authentikAvailability: AuthentikAvailability = {
  status: "unknown",
  checkedAt: null,
};

const updateAvailability = (status: AuthentikAvailability["status"], error?: string) => {
  authentikAvailability.status = status;
  authentikAvailability.checkedAt = Date.now();
  authentikAvailability.error = error;
};

const checkAuthentikAvailability = async () => {
  if (!authentikDiscoveryUrl) {
    updateAvailability("unreachable", "URL discovery для Authentik не задан");
    return authentikAvailability;
  }

  const controller = new AbortController();
  const timeoutMs = Math.max(authentikHttpTimeoutMs, 15000);
  const timeoutId = setTimeout(() => controller.abort(), timeoutMs);

  try {
    const response = await fetch(authentikDiscoveryUrl, {
      method: "GET",
      signal: controller.signal,
      cache: "no-store",
    });

    if (!response.ok) {
      throw new Error(`HTTP ${response.status} ${response.statusText}`);
    }

    updateAvailability("reachable");
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    updateAvailability("unreachable", message);
    console.warn("[auth-options] Провайдер Authentik недоступен", {
      url: authentikDiscoveryUrl,
      error: message,
    });
  } finally {
    clearTimeout(timeoutId);
  }

  return authentikAvailability;
};

const authentikAvailabilityCheck = checkAuthentikAvailability();

export const authentikDiscoveryEndpoint = authentikDiscoveryUrl;
export const getAuthentikAvailability = () => authentikAvailability;
export const waitForAuthentikAvailabilityCheck = () => authentikAvailabilityCheck;

type ExtendedJWT = JWT & {
  accessToken?: string;
  idToken?: string;
  refreshToken?: string;
  expiresAt?: number;
  profile?: Record<string, unknown> | null;
  refreshAttempts?: number;
  nextRefreshAttempt?: number;
  reauthRequired?: boolean;
};

type AuthentikTokenResponse = {
  access_token?: string;
  id_token?: string;
  refresh_token?: string;
  expires_in?: number;
};

type AuthentikTokenError = {
  error?: string;
  error_description?: string;
};

const refreshLeewayMs = 2 * 60 * 1000;
const refreshRetryBaseDelayMs = 5 * 1000;
const refreshRetryMaxDelayMs = 5 * 60 * 1000;
const defaultAccessTokenTtlMs = 10 * 60 * 1000;

const getJwtExpiration = (token?: string) => {
  if (typeof token !== "string") {
    return null;
  }

  const [, payload] = token.split(".");

  if (!payload) {
    return null;
  }

  try {
    const decodedPayload = JSON.parse(Buffer.from(payload, "base64").toString("utf8"));
    const exp = decodedPayload?.exp;

    return typeof exp === "number" ? exp * 1000 : null;
  } catch (error) {
    console.warn("[auth-options] Не удалось декодировать exp из JWT", error);
    return null;
  }
};

const resolveExpiresAt = ({
  expiresAtSeconds,
  expiresInSeconds,
  accessToken,
  idToken,
}: {
  expiresAtSeconds?: number | null;
  expiresInSeconds?: number | null;
  accessToken?: string | null;
  idToken?: string | null;
}) => {
  if (Number.isFinite(expiresAtSeconds)) {
    return (expiresAtSeconds as number) * 1000;
  }

  if (Number.isFinite(expiresInSeconds)) {
    return Date.now() + (expiresInSeconds as number) * 1000;
  }

  const jwtExpiration = getJwtExpiration(accessToken ?? undefined) ?? getJwtExpiration(idToken ?? undefined);

  if (jwtExpiration) {
    return jwtExpiration;
  }

  return accessToken ? Date.now() + defaultAccessTokenTtlMs : null;
};

const clearSession = (token: ExtendedJWT) => {
  token.accessToken = undefined;
  token.idToken = undefined;
  token.refreshToken = undefined;
  token.expiresAt = undefined;

  return token;
};

const markReauthRequired = (token: ExtendedJWT) => {
  const clearedToken = clearSession(token);

  clearedToken.reauthRequired = true;
  clearedToken.nextRefreshAttempt = undefined;
  clearedToken.refreshAttempts = undefined;

  return clearedToken;
};

const scheduleRefreshRetry = (token: ExtendedJWT) => {
  const attempts = (token.refreshAttempts ?? 0) + 1;
  const retryDelay = Math.min(refreshRetryMaxDelayMs, refreshRetryBaseDelayMs * 2 ** (attempts - 1));

  return {
    ...token,
    refreshAttempts: attempts,
    nextRefreshAttempt: Date.now() + retryDelay,
  } satisfies ExtendedJWT;
};

const refreshAccessToken = async (token: ExtendedJWT) => {
  if (token.reauthRequired) {
    return token;
  }

  const currentExpiresAt =
    token.expiresAt ??
    resolveExpiresAt({ accessToken: token.accessToken ?? null, idToken: token.idToken ?? null, expiresAtSeconds: null });

  if (!currentExpiresAt) {
    return markReauthRequired(token);
  }

  const normalizedToken: ExtendedJWT & { expiresAt: number } = { ...token, expiresAt: currentExpiresAt };

  if (!normalizedToken.refreshToken) {
    return markReauthRequired(normalizedToken);
  }

  const now = Date.now();
  const isExpired = normalizedToken.expiresAt <= now;
  const nextPlannedAttempt =
    typeof normalizedToken.nextRefreshAttempt === "number"
      ? normalizedToken.nextRefreshAttempt
      : normalizedToken.expiresAt - refreshLeewayMs;
  const shouldDelayRefresh = !isExpired && now < nextPlannedAttempt;

  if (shouldDelayRefresh) {
    return normalizedToken;
  }

  try {
    const params = new URLSearchParams({
      grant_type: "refresh_token",
      refresh_token: normalizedToken.refreshToken,
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

    const data = (await response.json().catch(() => null)) as
      | (AuthentikTokenResponse & AuthentikTokenError)
      | null;

    if (!response.ok) {
      if (data?.error === "invalid_grant") {
        return markReauthRequired(normalizedToken);
      }

      if (response.status >= 500) {
        console.error("Сетевой сбой или 5xx при обновлении токена Authentik", response.statusText);
        return scheduleRefreshRetry(normalizedToken);
      }

      console.error("Ошибка при обновлении токена Authentik", {
        status: response.status,
        statusText: response.statusText,
        error: data?.error,
      });

      return scheduleRefreshRetry(normalizedToken);
    }

    if (!data?.access_token) {
      console.error("Ответ Authentik не содержит access_token", { status: response.status });
      return scheduleRefreshRetry(normalizedToken);
    }

    const refreshedExpiresAt =
      resolveExpiresAt({
        expiresInSeconds: typeof data.expires_in === "number" ? data.expires_in : null,
        accessToken: data.access_token,
        idToken: data.id_token ?? normalizedToken.idToken ?? null,
      }) ?? null;

    if (!refreshedExpiresAt) {
      return markReauthRequired(normalizedToken);
    }

    const updatedToken: ExtendedJWT = {
      ...normalizedToken,
      accessToken: data.access_token,
      idToken: data.id_token ?? token.idToken,
      refreshToken: data.refresh_token ?? token.refreshToken,
      expiresAt: refreshedExpiresAt,
      refreshAttempts: 0,
      nextRefreshAttempt: undefined,
      reauthRequired: false,
    };

    return updatedToken;
  } catch (error) {
    console.error("Ошибка при обновлении токена Authentik", error);
    return scheduleRefreshRetry(normalizedToken);
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
        extendedToken.expiresAt =
          resolveExpiresAt({
            expiresAtSeconds: account.expires_at,
            accessToken: account.access_token,
            idToken: account.id_token,
          }) ?? undefined;
        extendedToken.reauthRequired = false;
        extendedToken.refreshAttempts = 0;
        extendedToken.nextRefreshAttempt = undefined;
        extendedToken.profile = profileData ?? extendedToken.profile ?? null;

        console.info("Получены новые токены из Authentik, сбрасываем статус reauthRequired", {
          hasAccessToken: Boolean(extendedToken.accessToken),
          reauthRequired: extendedToken.reauthRequired,
        });

        if (!extendedToken.expiresAt) {
          return markReauthRequired(extendedToken);
        }
      }

      return await refreshAccessToken(extendedToken);
    },
    async session({ session, token }) {
      const extendedToken = token as ExtendedJWT;

      if (extendedToken.reauthRequired) {
        return null;
      }

      session.accessToken = typeof extendedToken.accessToken === "string" ? extendedToken.accessToken : undefined;
      session.idToken = typeof extendedToken.idToken === "string" ? extendedToken.idToken : undefined;
      session.expiresAt = extendedToken.expiresAt;
      session.nextRefreshAttempt = extendedToken.nextRefreshAttempt;
      session.reauthRequired = extendedToken.reauthRequired ?? false;

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
