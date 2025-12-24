import { NextResponse, type NextRequest } from "next/server";
import { getServerSession } from "next-auth";
import { authOptions } from "@/lib/auth-options";
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

export async function POST(request: NextRequest) {
  const session = await getServerSession(authOptions);

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
    return NextResponse.json(
      { error: "Требуется повторный вход", code: "reauth_required" },
      { status: 440 },
    );
  }

  if (!activeSession || !activeSession.accessToken) {
    return NextResponse.json({ error: "Необходима активная сессия" }, { status: 401 });
  }

  if (!gatewayUrl) {
    return NextResponse.json({ error: "GATEWAY_URL не задан" }, { status: 500 });
  }

  const expiresAt = resolveSessionExpiresAt(activeSession);

  if (expiresAt !== null && expiresAt <= Date.now()) {
    return NextResponse.json(
      { error: "Требуется повторный вход", code: "reauth_required" },
      { status: 440 },
    );
  }

  const correlationId = request.headers.get("x-correlation-id") ?? generateCorrelationId();
  const body = await parseRequestBody(request);

  if (!body || typeof body !== "object") {
    return NextResponse.json({ error: "Некорректное тело запроса" }, { status: 400 });
  }

  try {
    console.info("GraphQL proxy forwarding request с accessToken", {
      correlationId,
      hasAccessToken: Boolean(activeSession.accessToken),
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

    return NextResponse.json(responsePayload, {
      status: upstream.status,
      headers: {
        "X-Correlation-ID": correlationId,
      },
    });
  } catch (error) {
    console.error("GraphQL proxy error", error);
    return NextResponse.json({ error: "Не удалось выполнить запрос к gateway" }, { status: 502 });
  }
}
