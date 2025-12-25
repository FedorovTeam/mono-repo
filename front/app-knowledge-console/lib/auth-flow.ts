'use client';

import { signIn } from 'next-auth/react';

import { createAuthLogger, type Logger } from './auth-logger';
import { generateCorrelationId } from './correlation';
import { setRestoringSession } from './session-restoration-store';

const interactionErrorCodes = [
  'interaction_required',
  'interactionrequiredautherror',
  'consent_required',
];
const retryableErrorCodes = ['login_required', 'session_expired'];
const MAX_SILENT_SIGN_IN_FAILURES = 2;

export class SilentSignInError extends Error {
  interactionRequired: boolean;
  retryable: boolean;
  code?: string | null;

  constructor(
    message: string,
    {
      interactionRequired = false,
      retryable = false,
      code,
    }: { interactionRequired?: boolean; retryable?: boolean; code?: string | null } = {},
  ) {
    super(message);
    this.name = 'SilentSignInError';
    this.interactionRequired = interactionRequired;
    this.retryable = retryable;
    this.code = code;
  }
}

const normalizeErrorCode = (error?: string | null) => {
  if (!error) return null;
  return error.toLowerCase();
};

export const mapAuthError = (error?: string | null) => {
  const normalized = normalizeErrorCode(error);

  return {
    code: normalized,
    interactionRequired: normalized ? interactionErrorCodes.includes(normalized) : false,
    retryable: normalized ? retryableErrorCodes.includes(normalized) : false,
  };
};

let activeAttempt: Promise<void> | null = null;
let failedAttempts = 0;
let navigationInProgress = false;

const normalizePath = (targetUrl?: string | null) => {
  const destination = new URL(targetUrl ?? window.location.href, window.location.href);

  return `${destination.pathname}${destination.search}${destination.hash}`;
};

const waitForNavigationCompletion = async (targetPath: string, logger: Logger): Promise<void> => {
  logger.debug('Ожидаем завершения навигации после silent sign-in.', { targetPath });

  await new Promise<void>((resolve) => {
    let completed = false;

    const cleanup = () => {
      if (completed) return;

      completed = true;
      navigationInProgress = false;
      window.removeEventListener('pagehide', handlePageHide);
      window.removeEventListener('beforeunload', handleBeforeUnload);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
      clearTimeout(timeoutId);
      resolve();
    };

    const finish = (reason: string) => {
      logger.debug('Завершение ожидания навигации после silent sign-in.', {
        targetPath,
        reason,
      });
      cleanup();
    };

    const handlePageHide = () => finish('pagehide');
    const handleBeforeUnload = () => finish('beforeunload');
    const handleVisibilityChange = () => {
      if (document.visibilityState === 'hidden') {
        finish('visibilitychange');
      }
    };

    const timeoutId = window.setTimeout(() => {
      logger.warn('Навигация после silent sign-in не подтверждена вовремя.', {
        targetPath,
      });
      finish('timeout');
    }, 5000);

    window.addEventListener('pagehide', handlePageHide, { once: true });
    window.addEventListener('beforeunload', handleBeforeUnload, { once: true });
    document.addEventListener('visibilitychange', handleVisibilityChange);
  });
};

const navigateAfterSilentSignIn = async (targetUrl: string | null | undefined, logger: Logger): Promise<void> => {
  const targetPath = normalizePath(targetUrl ?? window.location.href);
  const currentPath = normalizePath();

  if (targetPath === currentPath) {
    logger.info('Целевой URL совпадает с текущим, навигация не требуется.', {
      targetPath,
    });
    navigationInProgress = false;
    return;
  }

  navigationInProgress = true;
  logger.info('Выполняем навигацию после silent sign-in.', { targetPath });
  const navigationPromise = waitForNavigationCompletion(targetPath, logger);
  window.location.replace(targetPath);

  await navigationPromise;
};

const createSilentSignInAttempt = (context: string, logger: Logger): Promise<void> => {
  return new Promise((resolve, reject) => {
    signIn('authentik', {
      redirect: true,
      prompt: 'none',
      callbackUrl: window.location.href,
    })
      .then(async (signInResult) => {
        if (signInResult?.error) {
          const mappedError = mapAuthError(signInResult.error);
          logger.warn('Ошибка silent sign-in Authentik.', {
            context,
            error: signInResult.error,
            code: mappedError.code,
          });

          if (mappedError.interactionRequired) {
            reject(
              new SilentSignInError('Для продолжения войдите вручную.', {
                interactionRequired: true,
                code: mappedError.code,
              }),
            );
            return;
          }

          if (mappedError.retryable) {
            reject(
              new SilentSignInError('Сессия требует повторного входа.', {
                retryable: true,
                code: mappedError.code,
              }),
            );
            return;
          }

          reject(
            new SilentSignInError('Не удалось автоматически восстановить сессию.', {
              code: mappedError.code,
            }),
          );
          return;
        }

        logger.info('Silent sign-in выполнен успешно.', { context });

        if (signInResult?.url) {
          logger.debug('Получен URL завершения silent sign-in.', {
            context,
            url: signInResult.url,
          });
          await navigateAfterSilentSignIn(signInResult.url, logger);
          resolve();
          return;
        }

        await navigateAfterSilentSignIn(null, logger);
        resolve();
      })
      .catch((error) => {
        logger.error('Сбой silent sign-in Authentik.', { context, error });
        reject(new SilentSignInError('Не удалось автоматически восстановить сессию.'));
      });
  });
};

export const performSilentSignIn = async (
  context: string,
): Promise<void> => {
  if (typeof window === 'undefined') {
    throw new SilentSignInError('Silent sign-in доступен только в браузере.');
  }

  const correlationId = generateCorrelationId();
  const logger = createAuthLogger({
    correlationId,
    operation: 'silent sign-in',
    target: '/api/auth/signin/authentik',
  });

  if (activeAttempt) {
    logger.info('Обнаружена активная попытка silent sign-in, ожидаем.', { context });
    setRestoringSession(true);
    return activeAttempt;
  }

  logger.info('Запускаем silent sign-in.', { context });
  setRestoringSession(true);

  const attempt = (async () => {
    let attemptNumber = 0;

    while (attemptNumber < MAX_SILENT_SIGN_IN_FAILURES) {
      attemptNumber += 1;

      try {
        await createSilentSignInAttempt(context, logger);
        failedAttempts = 0;
        return;
      } catch (error) {
        failedAttempts += 1;

        if (error instanceof SilentSignInError) {
          logger.warn('Попытка silent sign-in не удалась.', {
            context,
            attemptNumber,
            failedAttempts,
            maxAttempts: MAX_SILENT_SIGN_IN_FAILURES,
            code: error.code,
            interactionRequired: error.interactionRequired,
            retryable: error.retryable,
          });

          if (error.interactionRequired) {
            throw error;
          }

          if (error.retryable && failedAttempts < MAX_SILENT_SIGN_IN_FAILURES) {
            const attemptsLeft = MAX_SILENT_SIGN_IN_FAILURES - failedAttempts;

            logger.info('Повторяем silent sign-in после возобновляемой ошибки.', {
              context,
              attemptNumber,
              failedAttempts,
              attemptsLeft,
            });
            continue;
          }

          if (error.retryable) {
            logger.warn('Достигнут лимит повторных попыток silent sign-in.', {
              context,
              attemptNumber,
              failedAttempts,
              maxAttempts: MAX_SILENT_SIGN_IN_FAILURES,
              code: error.code,
            });
          }
        }

        throw new SilentSignInError('Не удалось автоматически восстановить сессию.', {
          interactionRequired: failedAttempts >= MAX_SILENT_SIGN_IN_FAILURES,
        });
      }
    }

    throw new SilentSignInError('Превышено число попыток восстановления сессии.', {
      interactionRequired: true,
    });
  })()
    .catch((error) => {
      if (error instanceof SilentSignInError) {
        throw error;
      }

      throw new SilentSignInError('Не удалось автоматически восстановить сессию.');
    })
    .finally(() => {
      activeAttempt = null;
      setRestoringSession(false);
    });

  activeAttempt = attempt;

  return attempt;
};

export const hasActiveSilentSignIn = () => activeAttempt !== null;
export const isSilentSignInNavigationInProgress = () => navigationInProgress;
