Развертывание Authentik управляется через ArgoCD.

Создай приложение в ArgoCD:

```bash
kubectl apply -n argocd -f application.yaml
```

ArgoCD синхронизирует манифесты из каталога `manifests/authentik`.

## Настройка OAuth2 приложения для Argo Workflows

1. В интерфейсе Authentik перейдите в **Applications** → **Create**.
2. Создайте application с произвольным именем, тип `OAuth2 / OpenID Connect`.
3. В разделе **Provider** создайте новый OAuth2 Provider со значениями:
   - Redirect URIs: `https://<DOMAIN>/oauth2/callback`
   - Scopes: `openid profile email groups`
4. Сохраните `Client ID` и `Client Secret` — их нужно указать в `terragrunt.hcl` для Argo Workflows.
5. Включите выдачу групп (claim `groups`) и убедитесь, что в токене содержатся названия групп.

После создания приложения URL провайдера будет иметь вид `https://auth.example.com/application/o/<slug>/`.
Этот адрес используется как `oidc_issuer_url` в конфигурации Argo Workflows.
