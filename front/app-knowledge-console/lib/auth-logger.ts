import { createLogger, type Logger, type LoggerContext } from "./logger";

export type { LogLevel, Logger, LoggerContext } from "./logger";

export const createAuthLogger = (context: LoggerContext = {}): Logger =>
  createLogger({ scope: "auth", ...context });
