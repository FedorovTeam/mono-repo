'use client';

import { signIn } from 'next-auth/react';

import { setRestoringSession } from './session-restoration-store';

const interactionErrorCodes = [
  'interaction_required',
  'interactionrequiredautherror',
  'consent_required',
];
const retryableErrorCodes = ['login_required', 'session_expired'];
const MAX_SILENT_SIGN_IN_FAILURES = 1;

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

const waitForNavigationCompletion = async (targetPath: string): Promise<void> => {
  console.info('[auth-flow] Ожидаем завершения навигации после silent sign-in.', { targetPath });

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
      console.info('[auth-flow] Завершение ожидания навигации после silent sign-in.', {
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
      console.warn('[auth-flow] Навигация после silent sign-in не подтверждена вовремя.', {
        targetPath,
      });
      finish('timeout');
    }, 5000);

    window.addEventListener('pagehide', handlePageHide, { once: true });
    window.addEventListener('beforeunload', handleBeforeUnload, { once: true });
    document.addEventListener('visibilitychange', handleVisibilityChange);
  });
};

const navigateAfterSilentSignIn = async (targetUrl?: string | null): Promise<void> => {
  const targetPath = normalizePath(targetUrl ?? window.location.href);
  const currentPath = normalizePath();

  if (targetPath === currentPath) {
    console.info('[auth-flow] Целевой URL совпадает с текущим, навигация не требуется.', {
      targetPath,
    });
    navigationInProgress = false;
    return;
  }

  navigationInProgress = true;
  console.info('[auth-flow] Выполняем навигацию после silent sign-in.', { targetPath });
  const navigationPromise = waitForNavigationCompletion(targetPath);
  window.location.replace(targetPath);

  await navigationPromise;
};

const createSilentSignInAttempt = (context: string): Promise<void> => {
  return new Promise((resolve, reject) => {
    signIn('authentik', {
      redirect: true,
      prompt: 'none',
      callbackUrl: window.location.href,
    })
      .then(async (signInResult) => {
        if (signInResult?.error) {
          const mappedError = mapAuthError(signInResult.error);
          console.warn('[auth-flow] Ошибка silent sign-in Authentik.', {
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

        console.info('[auth-flow] Silent sign-in выполнен успешно.', { context });

        if (signInResult?.url) {
          console.info('[auth-flow] Получен URL завершения silent sign-in.', {
            context,
            url: signInResult.url,
          });
          await navigateAfterSilentSignIn(signInResult.url);
          resolve();
          return;
        }

        await navigateAfterSilentSignIn();
        resolve();
      })
      .catch((error) => {
        console.error('[auth-flow] Сбой silent sign-in Authentik.', { context, error });
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

  if (activeAttempt) {
    console.info('[auth-flow] Обнаружена активная попытка silent sign-in, ожидаем.', { context });
    setRestoringSession(true);
    return activeAttempt;
  }

  console.info('[auth-flow] Запускаем silent sign-in.', { context });
  setRestoringSession(true);

  const attempt = (async () => {
    let attemptNumber = 0;

    while (attemptNumber < MAX_SILENT_SIGN_IN_FAILURES) {
      attemptNumber += 1;

      try {
        await createSilentSignInAttempt(context);
        failedAttempts = 0;
        return;
      } catch (error) {
        failedAttempts += 1;

        if (error instanceof SilentSignInError) {
          console.warn('[auth-flow] Попытка silent sign-in не удалась.', {
            context,
            attemptNumber,
            failedAttempts,
            code: error.code,
            interactionRequired: error.interactionRequired,
            retryable: error.retryable,
          });

          if (error.interactionRequired) {
            throw error;
          }

          if (error.retryable && failedAttempts < MAX_SILENT_SIGN_IN_FAILURES) {
            continue;
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
