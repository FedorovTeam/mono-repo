import type { Mock } from "vitest";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getServerSession } from "next-auth";
import { getToken } from "next-auth/jwt";

import { POST } from "./route";

const { jsonMock } = vi.hoisted(() => ({
  jsonMock: vi.fn((body: unknown, init?: ResponseInit) => ({
    body,
    status: init?.status ?? 200,
  })),
}));

vi.mock("next/server", () => ({
  NextResponse: { json: jsonMock },
  NextRequest: class {},
}));

vi.mock("next-auth/jwt", () => ({
  getToken: vi.fn(),
}));

vi.mock("next-auth", () => ({
  getServerSession: vi.fn(),
}));

vi.mock("@/lib/auth-options", () => ({
  authOptions: {},
}));

vi.mock("@/lib/correlation", () => ({
  generateCorrelationId: () => "test-correlation-id",
}));

describe("POST /api/graphql", () => {
  afterEach(() => {
    vi.clearAllMocks();
  });

  it("возвращает 440, если токен помечен reauthRequired даже без server session", async () => {
    (getToken as Mock).mockResolvedValue({ reauthRequired: true });
    (getServerSession as Mock).mockResolvedValue(null);

    const request = {
      headers: new Headers(),
      json: vi.fn(),
    } as unknown as Request;

    const response = await POST(request as any);

    expect(jsonMock).toHaveBeenCalledWith(
      { error: "Требуется повторный вход", code: "reauth_required" },
      { status: 440 },
    );
    expect(response).toEqual({
      body: { error: "Требуется повторный вход", code: "reauth_required" },
      status: 440,
    });
    expect(getServerSession).not.toHaveBeenCalled();
  });
});
