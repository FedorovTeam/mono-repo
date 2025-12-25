import { NextResponse, type NextRequest } from "next/server";
import { getServerSession } from "next-auth";
import { getToken } from "next-auth/jwt";
import { authOptions } from "@/lib/auth-options";
import { createAuthLogger } from "@/lib/auth-logger";
import { generateCorrelationId } from "@/lib/correlation";

const gatewayUrl = process.env.GATEWAY_URL ?? process.env.NEXT_PUBLIC_GATEWAY_URL;

const getTokenExpirationMs = (token: string | undefined | null) => {
  if (!token) {
    return null;
  }

  const [, payload] = token.split(".");

  if (!payload) {
    return null;
  }

  try {
    const normalizedPayload = payload.replace(/-/g, "+").replace(/_/g, "/");
    const decodedPayload = Buffer.from(normalizedPayload, "base64").toString("utf8");
    const parsedPayload = JSON.parse(decodedPayload) as { exp?: number };

    return typeof parsedPayload.exp === "number" ? parsedPayload.exp * 1000 : null;
  } catch (error) {
    console.warn("Не удалось распарсить exp из accessToken", error);
    return null;
  }
};

const resolveSessionExpiresAt = (session: {
  expiresAt?: unknown;
  accessToken?: string | null;
}) => {
  if (typeof session.expiresAt === "number") {
    return session.expiresAt;
  }

  return getTokenExpirationMs(session.accessToken);
};

function parseRequestBody(request: NextRequest) {
  return request
    .json()
    .catch(() => null)
    .then((body) => body as unknown);
}

const resolveUserFromSession = (session: Awaited<ReturnType<typeof getServerSession>>) => {
  const idCandidate = (session?.user as { id?: string } | undefined)?.id;

  return {
    id: typeof idCandidate === "string" ? idCandidate : undefined,
    email: typeof session?.user?.email === "string" ? session.user.email : undefined,
  };
};

export async function POST(request: NextRequest) {
  const token = await getToken({ req: request });
  const correlationId = request.headers.get("x-correlation-id") ?? generateCorrelationId();
  const session = await getServerSession(authOptions);
  const logger = createAuthLogger({
    correlationId,
    operation: "GraphQL proxy",
    user: resolveUserFromSession(session),
    target: gatewayUrl,
  });

  if (token?.reauthRequired) {
    logger.info("Запрос прерван: требуется повторный вход", { code: "reauth_required" });
    return NextResponse.json(
      { error: "Требуется повторный вход", code: "reauth_required" },
      { status: 440 },
    );
  }

  const ensureActiveSession = async () => {
    if (!session || !session.accessToken) {
      return session;
    }

    const expiresAt = resolveSessionExpiresAt(session);

    if (expiresAt !== null && expiresAt > Date.now()) {
      return session;
    }

    return await getServerSession(authOptions);
  };

  const activeSession = await ensureActiveSession();

  if (activeSession?.reauthRequired) {
    logger.info("Серверная сессия требует повторного входа", { code: "reauth_required" });
    return NextResponse.json(
      { error: "Требуется повторный вход", code: "reauth_required" },
      { status: 440 },
    );
  }

  if (!activeSession || !activeSession.accessToken) {
    logger.warn("Запрос без действующего accessToken", { status: 401 });
    return NextResponse.json({ error: "Необходима активная сессия" }, { status: 401 });
  }

  if (!gatewayUrl) {
    logger.error("GATEWAY_URL не задан", { status: 500 });
    return NextResponse.json({ error: "GATEWAY_URL не задан" }, { status: 500 });
  }

  const expiresAt = resolveSessionExpiresAt(activeSession);

  if (expiresAt !== null && expiresAt <= Date.now()) {
    logger.info("Серверная сессия истекла", { expiresAt, status: 440 });
    return NextResponse.json(
      { error: "Требуется повторный вход", code: "reauth_required" },
      { status: 440 },
    );
  }

  const body = await parseRequestBody(request);

  if (!body || typeof body !== "object") {
    logger.warn("Некорректное тело GraphQL запроса", { status: 400 });
    return NextResponse.json({ error: "Некорректное тело запроса" }, { status: 400 });
  }

  try {
    const requestSummary = body as { operationName?: unknown; variables?: unknown };

    logger.info("GraphQL proxy отправляет запрос к gateway", {
      hasAccessToken: Boolean(activeSession.accessToken),
      operationName: typeof requestSummary.operationName === "string" ? requestSummary.operationName : undefined,
      variablesKeys:
        requestSummary.variables && typeof requestSummary.variables === "object"
          ? Object.keys(requestSummary.variables as Record<string, unknown>)
          : undefined,
    });

    const upstream = await fetch(gatewayUrl, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${activeSession.accessToken}`,
        "X-Correlation-ID": correlationId,
      },
      cache: "no-store",
      body: JSON.stringify(body),
    });

    const responseText = await upstream.text();
    const isJson = upstream.headers.get("content-type")?.includes("application/json");
    const responsePayload = isJson && responseText ? JSON.parse(responseText) : responseText;

    logger.info("GraphQL proxy получил ответ", {
      status: upstream.status,
      ok: upstream.ok,
      contentType: upstream.headers.get("content-type"),
    });

    return NextResponse.json(responsePayload, {
      status: upstream.status,
      headers: {
        "X-Correlation-ID": correlationId,
      },
    });
  } catch (error) {
    logger.error("GraphQL proxy error", { error });
    return NextResponse.json({ error: "Не удалось выполнить запрос к gateway" }, { status: 502 });
  }
}
