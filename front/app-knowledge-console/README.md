This is a [Next.js](https://nextjs.org) project bootstrapped with [`create-next-app`](https://nextjs.org/docs/app/api-reference/cli/create-next-app).

## Getting Started

First, run the development server:

```bash
npm run dev
# or
yarn dev
# or
pnpm dev
# or
bun dev
```

Open [http://localhost:3000](http://localhost:3000) with your browser to see the result.

You can start editing the page by modifying `app/page.tsx`. The page auto-updates as you edit the file.

This project uses [`next/font`](https://nextjs.org/docs/app/building-your-application/optimizing/fonts) to automatically optimize and load [Geist](https://vercel.com/font), a new font family for Vercel.

## Learn More

To learn more about Next.js, take a look at the following resources:

- [Next.js Documentation](https://nextjs.org/docs) - learn about Next.js features and API.
- [Learn Next.js](https://nextjs.org/learn) - an interactive Next.js tutorial.

You can check out [the Next.js GitHub repository](https://github.com/vercel/next.js) - your feedback and contributions are welcome!

## Deploy on Vercel

The easiest way to deploy your Next.js app is to use the [Vercel Platform](https://vercel.com/new?utm_medium=default-template&filter=next.js&utm_source=create-next-app&utm_campaign=create-next-app-readme) from the creators of Next.js.

Check out our [Next.js deployment documentation](https://nextjs.org/docs/app/building-your-application/deploying) for more details.

## Uploading knowledge documents

The upload form now builds the repository path automatically based on structured fields:

- **Category** – choose between:
  - _Пленарное заседание_ – requires the session number and document type:
    - **Стенограмма** – needs the transcript number, generating a path like `plenary/05/transcripts/stenogramma-03.md`.
    - **Протокол голосования** – needs the voting question number, generating a path like `plenary/05/votings/question-12.md`.
  - _Библиография_ → **Депутаты** – requires a slug/identifier, generating a path like `bibliography/deputies/ivanov-ivan.md`.

The computed path is shown as a preview and is sent to the GraphQL mutation; manual path entry is no longer required.

## Настройка аутентификации и BFF

Консоль использует OIDC-аутентификацию через Authentik и серверный прокси `/api/graphql`, который добавляет `Authorization: Bearer <access_token>` в запросы к gateway. Необходимые переменные окружения:

- `NEXTAUTH_URL` – базовый URL приложения.
- `NEXTAUTH_SECRET` – ключ для подписи сессий NextAuth.
- `AUTHENTIK_ISSUER`, `AUTHENTIK_CLIENT_ID`, `AUTHENTIK_CLIENT_SECRET`, `AUTHENTIK_REDIRECT_URI`, `AUTHENTIK_JWKS_URL` – параметры OIDC-провайдера Authentik (для контура console: issuer `https://auth.tech.fedorov-team.tech/application/o/console/`, client id `L6AmiswYzea2gAs5aGfzhLP9jlapG42ppPhtutoF`, secret `lGhLYG9Usi845ndGd7R8yEVuauxMKZQYhbBVmcuaPAbewC9pUvQPH5uhSiuSGKWq8nYydBoUDNnbngr38nxiecE1qgeoO7e4nrh3carhwG89a3xoHXENDa1XIfh3sUfP`, redirect `https://console.fedorov-team.tech/api/auth/callback/authentik`, JWKS `https://auth.tech.fedorov-team.tech/application/o/console/jwks/`).
- `AUTHENTIK_HTTP_TIMEOUT_MS` – таймаут HTTP-запросов к Authentik для discovery/token/userinfo (значение в мс, по умолчанию 12000). Для production рекомендуем 12000–15000 мс, для локальной разработки 10000–12000 мс, чтобы быстрее получать обратную связь при сбоях.
- `GATEWAY_URL` – внутренняя точка GraphQL gateway, вызываемая прокси.
- `NEXT_PUBLIC_AUTHENTIK_LOGOUT_URL` – endpoint logout Authentik (для контура console: `https://auth.tech.fedorov-team.tech/application/o/console/end-session/`).
- `NEXT_PUBLIC_AUTHENTIK_POST_LOGOUT_REDIRECT` – необязательно, редирект после logout (по умолчанию origin приложения; для контура console: `https://console.fedorov-team.tech`).

При очистке истёкших токенов серверный прокси возвращает статус `440` с кодом `reauth_required`, чтобы клиент мог сразу отправить пользователя на интерактивный вход вместо повторных silent sign-in.

## Smoke-тест silent sign-in с активной SSO-сессией

1. Залогиньтесь в Authentik в отдельной вкладке (SSO-сессия должна быть активна).
2. Откройте консоль по прямому URL (в проде `https://console.fedorov-team.tech/`, в dev — локальный адрес).
3. Убедитесь, что страница не зависает в ожидании токена: при наличии действующей SSO-сессии происходит мгновенный редирект туда-обратно и приложение открывается в авторизованном состоянии.
