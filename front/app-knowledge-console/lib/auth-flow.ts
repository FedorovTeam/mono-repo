'use client';

import type { AppRouterInstance } from 'next/dist/shared/lib/app-router-context.shared-runtime';
import { getSession, signIn } from 'next-auth/react';

import { setRestoringSession } from './session-restoration-store';

const interactionErrorCodes = [
  'interaction_required',
  'interactionrequiredautherror',
  'consent_required',
];
const retryableErrorCodes = ['login_required', 'session_expired'];
const SILENT_SIGN_IN_TIMEOUT_MS = 15_000;
const SESSION_POLL_TIMEOUT_MS = 5_000;
const SESSION_POLL_INTERVAL_MS = 300;
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

const replaceCurrentLocation = (
  router: AppRouterInstance | undefined,
  targetUrl?: string | null,
) => {
  const destination = new URL(targetUrl ?? window.location.href, window.location.href);
  const normalizedPath = `${destination.pathname}${destination.search}${destination.hash}`;

  if (router) {
    router.replace(normalizedPath);
    return;
  }

  window.history.replaceState(window.history.state, '', normalizedPath);
};

const waitForSessionAccessToken = async (context: string) => {
  const startedAt = Date.now();

  while (Date.now() - startedAt < SESSION_POLL_TIMEOUT_MS) {
    const session = await getSession();

    if (session?.accessToken) {
      return;
    }

    await new Promise((resolve) => setTimeout(resolve, SESSION_POLL_INTERVAL_MS));
  }

  console.error('[auth-flow] Не удалось дождаться обновлённой сессии после silent sign-in.', { context });
  throw new SilentSignInError('Сессия не была обновлена.', {
    retryable: true,
  });
};

const createSilentSignInAttempt = (context: string, router?: AppRouterInstance): Promise<void> => {
  return new Promise((resolve, reject) => {
    const timeoutId = setTimeout(() => {
      console.error('[auth-flow] Таймаут silent sign-in — отмена попытки.', { context });
      reject(
        new SilentSignInError('Таймаут восстановления сессии.', {
          retryable: true,
        }),
      );
    }, SILENT_SIGN_IN_TIMEOUT_MS);

    const clearTimer = () => clearTimeout(timeoutId);

    signIn('authentik', {
      redirect: false,
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
            clearTimer();
            reject(
              new SilentSignInError('Для продолжения войдите вручную.', {
                interactionRequired: true,
                code: mappedError.code,
              }),
            );
            return;
          }

          if (mappedError.retryable) {
            clearTimer();
            reject(
              new SilentSignInError('Сессия требует повторного входа.', {
                retryable: true,
                code: mappedError.code,
              }),
            );
            return;
          }

          clearTimer();
          reject(
            new SilentSignInError('Не удалось автоматически восстановить сессию.', {
              code: mappedError.code,
            }),
          );
          return;
        }

        console.info('[auth-flow] Silent sign-in выполнен успешно.', { context });

        if (signInResult?.url) {
          console.info('[auth-flow] Получен URL завершения silent sign-in, выполняем навигацию.', {
            context,
          });
          window.location.replace(signInResult.url);
        } else {
          await waitForSessionAccessToken(context);
          clearTimer();
          replaceCurrentLocation(router, signInResult?.url);
          resolve();
        }
      })
      .catch((error) => {
        clearTimer();
        console.error('[auth-flow] Сбой silent sign-in Authentik.', { context, error });
        reject(new SilentSignInError('Не удалось автоматически восстановить сессию.'));
      });
  });
};

export const performSilentSignIn = async (
  context: string,
  { router }: { router?: AppRouterInstance } = {},
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
        await createSilentSignInAttempt(context, router);
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
