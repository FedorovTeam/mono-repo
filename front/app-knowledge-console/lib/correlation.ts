// front/app-knowledge-console/lib/correlation.ts
export function generateCorrelationId() {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }

  return `corr-${Date.now()}`;
}
