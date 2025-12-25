// front/app-knowledge-console/app/api/auth/provider-status/route.ts
import { NextResponse } from "next/server";

import {
  authentikDiscoveryEndpoint,
  getAuthentikAvailability,
  waitForAuthentikAvailabilityCheck,
} from "@/lib/auth-options";

export async function GET() {
  await waitForAuthentikAvailabilityCheck().catch(() => null);

  const availability = getAuthentikAvailability();

  return NextResponse.json({
    provider: "authentik",
    status: availability.status,
    reachable: availability.status === "reachable",
    checkedAt: availability.checkedAt,
    error: availability.error ?? null,
    discoveryUrl: authentikDiscoveryEndpoint,
  });
}
