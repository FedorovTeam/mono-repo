'use client';

import { signIn } from 'next-auth/react';

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

const replaceCurrentLocation = (targetUrl?: string | null) => {
  const destination = new URL(targetUrl ?? window.location.href, window.location.href);
  const normalizedPath = `${destination.pathname}${destination.search}${destination.hash}`;

  window.location.replace(normalizedPath);
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
          console.info('[auth-flow] Получен URL завершения silent sign-in, выполняем навигацию.', {
            context,
          });
          replaceCurrentLocation(signInResult.url);
          resolve();
          return;
        }

        replaceCurrentLocation();
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

            console.info('[auth-flow] Повторяем silent sign-in после возобновляемой ошибки.', {
              context,
              attemptNumber,
              failedAttempts,
              attemptsLeft,
            });
            continue;
          }

          if (error.retryable) {
            console.warn('[auth-flow] Достигнут лимит повторных попыток silent sign-in.', {
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
