// front/app-knowledge-console/lib/logger.ts
import { generateCorrelationId } from "./correlation";

export type LogLevel = "info" | "debug" | "trace" | "warn" | "error";

export type LoggerContext = {
  correlationId?: string;
  operation?: string;
  user?: {
    id?: string | null;
    email?: string | null;
  };
  target?: string | null;
  scope?: string;
};

type LogMethodContext = LoggerContext & Record<string, unknown>;

export type Logger = {
  info: (message: string, context?: LogMethodContext) => void;
  debug: (message: string, context?: LogMethodContext) => void;
  trace: (message: string, context?: LogMethodContext) => void;
  warn: (message: string, context?: LogMethodContext) => void;
  error: (message: string, context?: LogMethodContext) => void;
};

const isDebugEnabled =
  process.env.APP_DEBUG_LOG === "true" || process.env.AUTH_DEBUG_LOG === "true";

const baseConsole: Record<Exclude<LogLevel, "trace">, (message?: unknown, ...optionalParams: unknown[]) => void> = {
  info: console.info,
  debug: console.debug,
  warn: console.warn,
  error: console.error,
};

const shouldLog = (level: LogLevel) => {
  if (level === "trace" || level === "debug") {
    return isDebugEnabled;
  }

  return true;
};

const normalizeContext = (context: LoggerContext, extra?: LogMethodContext) => {
  const merged = {
    correlationId: context.correlationId ?? extra?.correlationId ?? generateCorrelationId(),
    operation: context.operation ?? extra?.operation,
    user: context.user ?? extra?.user,
    target: context.target ?? extra?.target,
    scope: context.scope ?? extra?.scope ?? "app",
    ...(extra ?? {}),
  } as LogMethodContext;

  return merged;
};

export const createLogger = (context: LoggerContext = {}): Logger => {
  const baseContext = {
    ...context,
    correlationId: context.correlationId ?? generateCorrelationId(),
    scope: context.scope ?? "app",
  } satisfies LoggerContext;

  const log = (level: LogLevel, message: string, extra?: LogMethodContext) => {
    if (!shouldLog(level)) return;

    const normalized = normalizeContext(baseContext, extra);
    const { correlationId, operation, user, target, scope, ...rest } = normalized;
    const payload = {
      correlationId,
      operation,
      user,
      target,
      scope,
      ...rest,
    };

    const prefixScope = scope ?? "app";

    if (level === "trace") {
      baseConsole.debug(`[${prefixScope}-trace] ${message}`.trim(), payload);
      return;
    }

    const consoleMethod = baseConsole[level];
    consoleMethod?.(`[${prefixScope}-${level}] ${message}`.trim(), payload);
  };

  return {
    info: (message, extra) => log("info", message, extra),
    debug: (message, extra) => log("debug", message, extra),
    trace: (message, extra) => log("trace", message, extra),
    warn: (message, extra) => log("warn", message, extra),
    error: (message, extra) => log("error", message, extra),
  } satisfies Logger;
};
